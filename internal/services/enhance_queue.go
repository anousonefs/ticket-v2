package services

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"runtime"
	"sync"
	"sync/atomic"
	"ticket-system/config"
	"ticket-system/models"
	"ticket-system/monitoring"
	"time"

	pubnub "github.com/pubnub/go"
	"github.com/redis/go-redis/v9"
)

type EnhancedQueueService struct {
	Redis            *redis.Client
	pubnub           *pubnub.PubNub
	config           *config.Config
	monitor          *monitoring.Monitor
	stopChan         chan struct{}
	wg               sync.WaitGroup
	activeGoroutines int64 // Atomic counter for goroutines
}

func NewEnhancedQueueService(redisClient *redis.Client, pn *pubnub.PubNub, cfg *config.Config) *EnhancedQueueService {
	monitor := monitoring.NewMonitor(redisClient)

	service := &EnhancedQueueService{
		Redis:    redisClient,
		pubnub:   pn,
		config:   cfg,
		monitor:  monitor,
		stopChan: make(chan struct{}),
	}

	// Start background services with proper goroutine tracking
	service.startBackgroundServices()

	return service
}

func (s *EnhancedQueueService) startBackgroundServices() {
	// Start timeout manager - ONLY 1 goroutine for all timeouts
	s.wg.Add(1)
	go s.timeoutManager()

	// Start position updater - ONLY 1 goroutine for all position updates
	s.wg.Add(1)
	go s.positionUpdater()

	// Start health monitor - ONLY 1 goroutine for health checks
	s.wg.Add(1)
	go s.healthMonitor()

	log.Printf("Started %d background goroutines", 3)
}

// Centralized timeout manager - handles ALL events and users
func (s *EnhancedQueueService) timeoutManager() {
	defer s.wg.Done()
	atomic.AddInt64(&s.activeGoroutines, 1)
	defer atomic.AddInt64(&s.activeGoroutines, -1)

	ticker := time.NewTicker(15 * time.Second) // Check every 15 seconds
	defer ticker.Stop()

	log.Println("Timeout manager started")

	for {
		select {
		case <-ticker.C:
			s.checkAllTimeouts()
		case <-s.stopChan:
			log.Println("Timeout manager stopping")
			return
		}
	}
}

func (s *EnhancedQueueService) checkAllTimeouts() {
	ctx := context.Background()
	timeoutCount := 0

	// Get all processing queues across ALL events
	keys, err := s.Redis.Keys(ctx, "queue:processing:*").Result()
	if err != nil {
		log.Printf("Error getting processing keys: %v", err)
		return
	}

	for _, key := range keys {
		eventID := key[len("queue:processing:"):]

		// Get all processing users for this event
		members, err := s.Redis.SMembers(ctx, key).Result()
		if err != nil {
			continue
		}

		for _, member := range members {
			var user models.ProcessingUser
			if err := json.Unmarshal([]byte(member), &user); err != nil {
				continue
			}

			// Check if timeout exceeded
			if time.Since(user.StartedAt) > s.config.SeatLockTimeout {
				log.Printf("Timeout detected: user %s in event %s (%.1f minutes)",
					user.UserID, eventID, time.Since(user.StartedAt).Minutes())

				s.RemoveFromProcessing(ctx, eventID, user.UserID)
				s.monitor.TrackQueueOperation("timeout", eventID, "success")
				timeoutCount++

				// Process next user in queue for this event
				// Use a separate goroutine with tracking
				s.wg.Add(1)
				go func(eventID string) {
					defer s.wg.Done()
					atomic.AddInt64(&s.activeGoroutines, 1)
					defer atomic.AddInt64(&s.activeGoroutines, -1)

					s.ProcessQueue(ctx, eventID)
				}(eventID)
			}
		}
	}

	if timeoutCount > 0 {
		log.Printf("Processed %d timeouts across %d events", timeoutCount, len(keys))
	}
}

func (s *EnhancedQueueService) RemoveFromProcessing(ctx context.Context, eventID, userID string) error {
	processingKey := fmt.Sprintf("queue:processing:%s", eventID)

	// Get all processing entries
	members, err := s.Redis.SMembers(ctx, processingKey).Result()
	if err != nil {
		return err
	}

	// Find and remove the user
	for _, member := range members {
		var user models.ProcessingUser
		if err := json.Unmarshal([]byte(member), &user); err != nil {
			continue
		}

		if user.UserID == userID {
			// Remove from processing set
			s.Redis.SRem(ctx, processingKey, member)

			// Release locked seats
			seatService := NewSeatService(s.Redis)
			for _, seatID := range user.LockedSeats {
				seatService.UnlockSeat(ctx, eventID, seatID)
			}

			// Clean up user status
			userKey := fmt.Sprintf("user:queue:%s:%s", eventID, userID)
			s.Redis.Del(ctx, userKey)

			// Notify user they were timed out
			channel := fmt.Sprintf("user-%s", userID)
			s.pubnub.Publish().
				Channel(channel).
				Message(map[string]any{
					"type":     "processing_timeout",
					"event_id": eventID,
					"message":  "Your session has timed out. Please rejoin the queue.",
				}).
				Execute()

			log.Printf("User %s removed from processing for event %s", userID, eventID)
			break
		}
	}

	return nil
}

// Position updater - handles ALL events
func (s *EnhancedQueueService) positionUpdater() {
	defer s.wg.Done()
	atomic.AddInt64(&s.activeGoroutines, 1)
	defer atomic.AddInt64(&s.activeGoroutines, -1)

	ticker := time.NewTicker(s.config.QueuePositionUpdate)
	defer ticker.Stop()

	log.Println("Position updater started")

	for {
		select {
		case <-ticker.C:
			s.updateAllPositions()
		case <-s.stopChan:
			log.Println("Position updater stopping")
			return
		}
	}
}

func (s *EnhancedQueueService) updateAllPositions() {
	ctx := context.Background()

	// Get all waiting queues
	keys, err := s.Redis.Keys(ctx, "queue:waiting:*").Result()
	if err != nil {
		log.Printf("Error getting queue keys: %v", err)
		return
	}

	totalUsers := 0

	for _, key := range keys {
		eventID := key[len("queue:waiting:"):]

		entries, err := s.Redis.LRange(ctx, key, 0, -1).Result()
		if err != nil {
			continue
		}

		totalUsers += len(entries)

		// Update positions for this event
		for i, entryData := range entries {
			var entry models.QueueEntry
			if err := json.Unmarshal([]byte(entryData), &entry); err != nil {
				continue
			}

			position := len(entries) - i

			// Store position with TTL
			posKey := fmt.Sprintf("queue:position:%s:%s", eventID, entry.UserID)
			s.Redis.Set(ctx, posKey, position, 15*time.Second)

			// Notify user (throttled notifications)
			if s.shouldNotifyPosition(position) {
				s.notifyUserPosition(entry.UserID, eventID, position)
			}
		}
	}

	if len(keys) > 0 {
		log.Printf("Updated positions for %d users across %d events", totalUsers, len(keys))
	}
}

func (s *EnhancedQueueService) shouldNotifyPosition(position int) bool {
	// Notify more frequently for users closer to front
	if position <= 5 {
		return true // Always notify top 5
	} else if position <= 20 {
		return position%2 == 0 // Every 2nd position for top 20
	} else if position <= 100 {
		return position%10 == 0 // Every 10th position for top 100
	}
	return position%50 == 0 // Every 50th position for others
}

func (s *EnhancedQueueService) notifyUserPosition(userID, eventID string, position int) {
	message := fmt.Sprintf("You are #%d in line", position)
	if position == 1 {
		message = "You're next! 🎉"
	} else if position <= 5 {
		message = fmt.Sprintf("Almost there! You're #%d", position)
	}

	channel := fmt.Sprintf("user-%s", userID)
	s.pubnub.Publish().
		Channel(channel).
		Message(map[string]interface{}{
			"type":     "queue_position",
			"position": position,
			"event_id": eventID,
			"message":  message,
		}).
		Execute()
}

// Health monitor
func (s *EnhancedQueueService) healthMonitor() {
	defer s.wg.Done()
	atomic.AddInt64(&s.activeGoroutines, 1)
	defer atomic.AddInt64(&s.activeGoroutines, -1)

	ticker := time.NewTicker(1 * time.Minute)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			s.logHealthStats()
		case <-s.stopChan:
			return
		}
	}
}

func (s *EnhancedQueueService) logHealthStats() {
	ctx := context.Background()

	// Count active queues
	waitingKeys, _ := s.Redis.Keys(ctx, "queue:waiting:*").Result()
	processingKeys, _ := s.Redis.Keys(ctx, "queue:processing:*").Result()

	totalWaiting := 0
	totalProcessing := 0

	for _, key := range waitingKeys {
		count, _ := s.Redis.LLen(ctx, key).Result()
		totalWaiting += int(count)
	}

	for _, key := range processingKeys {
		count, _ := s.Redis.SCard(ctx, key).Result()
		totalProcessing += int(count)
	}

	goroutines := atomic.LoadInt64(&s.activeGoroutines)
	memStats := &runtime.MemStats{}
	runtime.ReadMemStats(memStats)

	log.Printf("Health Stats - Events: %d, Waiting: %d, Processing: %d, Goroutines: %d, Memory: %.1fMB",
		len(waitingKeys), totalWaiting, totalProcessing, goroutines,
		float64(memStats.Alloc)/1024/1024)
}

// Enhanced ProcessQueue with proper goroutine management
func (s *EnhancedQueueService) ProcessQueue(ctx context.Context, eventID string) {
	processingKey := fmt.Sprintf("queue:processing:%s", eventID)
	waitingKey := fmt.Sprintf("queue:waiting:%s", eventID)

	processedCount := 0

	for {
		// Check current processing count
		processingCount, err := s.Redis.SCard(ctx, processingKey).Result()
		if err != nil {
			log.Printf("Error getting processing count for event %s: %v", eventID, err)
			break
		}

		if processingCount >= int64(s.config.MaxProcessingUsers) {
			log.Printf("Processing queue full for event %s (%d/%d)",
				eventID, processingCount, s.config.MaxProcessingUsers)
			break
		}

		// Get next user from waiting queue
		data, err := s.Redis.RPop(ctx, waitingKey).Result()
		if err == redis.Nil {
			if processedCount > 0 {
				log.Printf("Processed %d users for event %s - queue now empty", processedCount, eventID)
			}
			break
		} else if err != nil {
			log.Printf("Error popping from queue for event %s: %v", eventID, err)
			break
		}

		var entry models.QueueEntry
		if err := json.Unmarshal([]byte(data), &entry); err != nil {
			log.Printf("Error unmarshaling queue entry: %v", err)
			continue
		}

		// Validate session
		if !s.validateUserSession(ctx, eventID, entry.UserID, entry.SessionID) {
			log.Printf("Invalid session for user %s, skipping", entry.UserID)
			continue
		}

		// Move to processing
		if s.moveUserToProcessing(ctx, eventID, entry) {
			processedCount++
			s.monitor.TrackQueueOperation("process", eventID, "success")
		}
	}
}

func (s *EnhancedQueueService) validateUserSession(ctx context.Context, eventID, userID, sessionID string) bool {
	userKey := fmt.Sprintf("user:queue:%s:%s", eventID, userID)
	storedSessionID, err := s.Redis.HGet(ctx, userKey, "session_id").Result()
	return err == nil && storedSessionID == sessionID
}

func (s *EnhancedQueueService) moveUserToProcessing(ctx context.Context, eventID string, entry models.QueueEntry) bool {
	processingKey := fmt.Sprintf("queue:processing:%s", eventID)

	processingUser := models.ProcessingUser{
		UserID:    entry.UserID,
		EventID:   entry.EventID,
		StartedAt: time.Now(),
		SessionID: entry.SessionID,
	}

	processingData, _ := json.Marshal(processingUser)
	err := s.Redis.SAdd(ctx, processingKey, processingData).Err()
	if err != nil {
		log.Printf("Error adding user %s to processing: %v", entry.UserID, err)
		return false
	}

	// Update user status
	userKey := fmt.Sprintf("user:queue:%s:%s", eventID, entry.UserID)
	s.Redis.HSet(ctx, userKey, "status", "processing")

	// Notify user
	s.notifyUserProcessing(entry.UserID, eventID)

	log.Printf("User %s moved to processing for event %s", entry.UserID, eventID)
	return true
}

func (s *EnhancedQueueService) notifyUserProcessing(userID, eventID string) {
	channel := fmt.Sprintf("user-%s", userID)
	s.pubnub.Publish().
		Channel(channel).
		Message(map[string]interface{}{
			"type":     "queue_status",
			"status":   "processing",
			"event_id": eventID,
			"message":  "🎉 You can now select your seats!",
		}).
		Execute()
}

// Graceful shutdown with proper goroutine cleanup
func (s *EnhancedQueueService) Shutdown() {
	log.Println("Shutting down queue service...")

	// Signal all goroutines to stop
	close(s.stopChan)

	// Wait for all goroutines to finish with timeout
	done := make(chan struct{})
	go func() {
		s.wg.Wait()
		close(done)
	}()

	select {
	case <-done:
		log.Println("All goroutines stopped gracefully")
	case <-time.After(30 * time.Second):
		log.Println("Timeout waiting for goroutines to stop")
	}

	finalCount := atomic.LoadInt64(&s.activeGoroutines)
	log.Printf("Queue service shutdown complete. Final goroutine count: %d", finalCount)
}
