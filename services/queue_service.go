// services/queue_service.go
package services

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"sync"
	"ticket-system/config"
	"ticket-system/models"
	"time"

	pubnub "github.com/pubnub/go"
	"github.com/redis/go-redis/v9"
)

type QueueService struct {
	Redis  *redis.Client
	PubNub *pubnub.PubNub
	Config *config.Config
}

func NewQueueService(redisClient *redis.Client, pn *pubnub.PubNub, cfg *config.Config) *QueueService {
	return &QueueService{
		Redis:  redisClient,
		PubNub: pn,
		Config: cfg,
	}
}

// UpdateQueuePositions - Improved version with batch updates and optimizations
func (s *QueueService) UpdateQueuePositions(ctx context.Context) {
	ticker := time.NewTicker(s.Config.QueuePositionUpdate)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			log.Println("UpdateQueuePositions: Context cancelled, stopping...")
			return
		case <-ticker.C:
			s.processQueuePositionUpdates(ctx)
		}
	}
}

// processQueuePositionUpdates handles the actual position update logic
func (s *QueueService) processQueuePositionUpdates(ctx context.Context) {
	// Get active events from set instead of using Keys()
	eventIDs, err := s.Redis.SMembers(ctx, "active_events").Result()
	if err != nil {
		log.Printf("Error getting active events: %v", err)
		return
	}

	// Process each event's queue in parallel with controlled concurrency
	var wg sync.WaitGroup
	semaphore := make(chan struct{}, 10) // Limit concurrent processing to 10 events

	for _, eventID := range eventIDs {
		wg.Add(1)
		semaphore <- struct{}{} // Acquire semaphore

		go func(eventID string) {
			defer wg.Done()
			defer func() { <-semaphore }() // Release semaphore

			s.updateEventQueuePositions(ctx, eventID)
		}(eventID)
	}

	wg.Wait()
}

// updateEventQueuePositions updates positions for a specific event
func (s *QueueService) updateEventQueuePositions(ctx context.Context, eventID string) {
	queueKey := fmt.Sprintf("queue:waiting:%s", eventID)

	// Get all entries from head to tail
	entries, err := s.Redis.LRange(ctx, queueKey, 0, -1).Result()
	if err != nil {
		log.Printf("Error getting queue entries for event %s: %v", eventID, err)
		return
	}

	// If queue is empty, remove from active events
	if len(entries) == 0 {
		s.Redis.SRem(ctx, "active_events", eventID)
		// Clean up queue metrics
		s.Redis.Del(ctx, fmt.Sprintf("queue:metrics:%s", eventID))
		return
	}

	// Prepare batch updates
	updates := make([]map[string]any, 0, len(entries))
	positionUpdates := make(map[string]int)
	var totalWaitTime time.Duration

	// Process each entry
	for i, entryData := range entries {
		var entry models.QueueEntry
		if err := json.Unmarshal([]byte(entryData), &entry); err != nil {
			log.Printf("Error unmarshaling queue entry: %v", err)
			continue
		}

		// Position is i + 1 (1-based indexing for user display)
		position := i + 1
		positionUpdates[entry.UserID] = position

		// Calculate wait time for metrics
		waitTime := time.Since(entry.JoinedAt)
		totalWaitTime += waitTime

		// Cache position in Redis with pipeline for better performance
		posKey := fmt.Sprintf("queue:position:%s:%s", eventID, entry.UserID)
		s.Redis.Set(ctx, posKey, position, 5*time.Second)

		// Add to batch update
		updates = append(updates, map[string]any{
			"user_id":   entry.UserID,
			"position":  position,
			"wait_time": int(waitTime.Seconds()),
		})
	}

	// Send batch update via PubNub
	if len(updates) > 0 {
		// Send to event channel for all users watching this event
		eventChannel := fmt.Sprintf("event-%s-queue", eventID)
		s.PubNub.Publish().
			Channel(eventChannel).
			Message(map[string]any{
				"type":     "queue_positions_batch",
				"event_id": eventID,
				"updates":  updates,
				"total":    len(entries),
			}).
			Execute()

	}

	// Update queue metrics
	s.updateQueueMetrics(ctx, eventID, len(entries), totalWaitTime)
}

// updateQueueMetrics updates queue metrics for monitoring
func (s *QueueService) updateQueueMetrics(ctx context.Context, eventID string, queueLength int, totalWaitTime time.Duration) {
	avgWaitTime := float64(0)
	if queueLength > 0 {
		avgWaitTime = totalWaitTime.Seconds() / float64(queueLength)
	}

	metricsKey := fmt.Sprintf("queue:metrics:%s", eventID)
	s.Redis.HSet(ctx, metricsKey, map[string]any{
		"total_in_queue": queueLength,
		"avg_wait_time":  avgWaitTime,
		"last_updated":   time.Now().Unix(),
	})

	// Set expiration for metrics (24 hours)
	s.Redis.Expire(ctx, metricsKey, 24*time.Hour)

	// Log metrics for monitoring
	if queueLength > 100 { // Alert for long queues
		log.Printf("ALERT: Event %s has %d users in queue with avg wait time: %.2f seconds",
			eventID, queueLength, avgWaitTime)
	}
}

// EnqueueUser - Enhanced version that adds event to active set
func (s *QueueService) EnqueueUser(ctx context.Context, eventID, userID, sessionID string) error {
	entry := models.QueueEntry{
		UserID:    userID,
		EventID:   eventID,
		JoinedAt:  time.Now(),
		Status:    "waiting",
		SessionID: sessionID,
	}

	data, err := json.Marshal(entry)
	if err != nil {
		return err
	}

	queueKey := fmt.Sprintf("queue:waiting:%s", eventID)

	// Use pipeline for atomic operations
	pipe := s.Redis.Pipeline()

	// Check if queue was empty before adding
	queueLenCmd := pipe.LLen(ctx, queueKey)

	// Add to queue
	pipe.LPush(ctx, queueKey, data)

	// Add event to active events set
	pipe.SAdd(ctx, "active_events", eventID)

	// Store user queue status
	userKey := fmt.Sprintf("user:queue:%s:%s", eventID, userID)
	pipe.HSet(ctx, userKey, map[string]any{
		"status":     "waiting",
		"joined_at":  time.Now().Unix(),
		"session_id": sessionID,
	})

	// Execute pipeline
	_, err = pipe.Exec(ctx)
	if err != nil {
		return err
	}

	// If queue was empty, trigger processing
	if queueLenCmd.Val() == 0 {
		go s.ProcessQueue(ctx, eventID)
	}

	// Send immediate position update
	s.sendQueueJoinNotification(eventID, userID)

	return nil
}

// sendQueueJoinNotification sends an immediate notification when user joins queue
func (s *QueueService) sendQueueJoinNotification(eventID, userID string) {
	channel := fmt.Sprintf("user-%s", userID)
	s.PubNub.Publish().
		Channel(channel).
		Message(map[string]any{
			"type":     "queue_joined",
			"event_id": eventID,
			"message":  "You have successfully joined the queue",
		}).
		Execute()
}

// GetQueueMetrics returns current queue metrics for an event
func (s *QueueService) GetQueueMetrics(ctx context.Context, eventID string) (map[string]any, error) {
	metricsKey := fmt.Sprintf("queue:metrics:%s", eventID)
	metrics, err := s.Redis.HGetAll(ctx, metricsKey).Result()
	if err != nil {
		return nil, err
	}

	// Get current processing count
	processingKey := fmt.Sprintf("queue:processing:%s", eventID)
	processingCount, _ := s.Redis.SCard(ctx, processingKey).Result()

	result := make(map[string]any)
	for k, v := range metrics {
		result[k] = v
	}
	result["processing_count"] = processingCount

	return result, nil
}

// CleanupInactiveQueues removes inactive queues and their data
func (s *QueueService) CleanupInactiveQueues(ctx context.Context) {
	ticker := time.NewTicker(1 * time.Hour)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.performQueueCleanup(ctx)
		}
	}
}

// performQueueCleanup performs the actual cleanup
func (s *QueueService) performQueueCleanup(ctx context.Context) {
	eventIDs, err := s.Redis.SMembers(ctx, "active_events").Result()
	if err != nil {
		log.Printf("Error getting active events for cleanup: %v", err)
		return
	}

	for _, eventID := range eventIDs {
		queueKey := fmt.Sprintf("queue:waiting:%s", eventID)
		queueLen, _ := s.Redis.LLen(ctx, queueKey).Result()

		// If queue is empty, check how long it's been empty
		if queueLen == 0 {
			metricsKey := fmt.Sprintf("queue:metrics:%s", eventID)
			lastUpdated, _ := s.Redis.HGet(ctx, metricsKey, "last_updated").Int64()

			// If no update for more than 1 hour, remove from active events
			if time.Now().Unix()-lastUpdated > 3600 {
				s.Redis.SRem(ctx, "active_events", eventID)
				s.Redis.Del(ctx, metricsKey)
				log.Printf("Removed inactive event %s from active events", eventID)
			}
		}
	}
}

// ProcessQueue - Enhanced version from the original
func (s *QueueService) ProcessQueue(ctx context.Context, eventID string) {
	processingKey := fmt.Sprintf("queue:processing:%s", eventID)
	waitingKey := fmt.Sprintf("queue:waiting:%s", eventID)

	for {
		// Check current processing count
		processingCount, err := s.Redis.SCard(ctx, processingKey).Result()
		if err != nil {
			log.Printf("Error getting processing count: %v", err)
			break
		}

		// Check if we can add more users to processing
		if processingCount >= int64(s.Config.MaxProcessingUsers) {
			break
		}

		// Get next user from waiting queue
		data, err := s.Redis.RPop(ctx, waitingKey).Result()
		if err == redis.Nil {
			// Queue is empty
			break
		} else if err != nil {
			log.Printf("Error popping from queue: %v", err)
			break
		}

		var entry models.QueueEntry
		if err := json.Unmarshal([]byte(data), &entry); err != nil {
			log.Printf("Error unmarshaling queue entry: %v", err)
			continue
		}

		// Move to processing with pipeline
		pipe := s.Redis.Pipeline()

		processingUser := models.ProcessingUser{
			UserID:    entry.UserID,
			EventID:   entry.EventID,
			StartedAt: time.Now(),
			SessionID: entry.SessionID,
		}

		processingData, _ := json.Marshal(processingUser)
		pipe.SAdd(ctx, processingKey, processingData)

		// Update user status
		userKey := fmt.Sprintf("user:queue:%s:%s", eventID, entry.UserID)
		pipe.HSet(ctx, userKey, "status", "processing")

		_, err = pipe.Exec(ctx)
		if err != nil {
			log.Printf("Error moving user to processing: %v", err)
			continue
		}

		// Notify user via PubNub
		channel := fmt.Sprintf("user-%s", entry.UserID)
		s.PubNub.Publish().
			Channel(channel).
			Message(map[string]any{
				"type":     "queue_status",
				"status":   "processing",
				"event_id": eventID,
			}).
			Execute()

		// Set processing timeout
		go s.setProcessingTimeout(ctx, eventID, entry.UserID)

		// Log for monitoring
		log.Printf("User %s moved to processing for event %s", entry.UserID, eventID)
	}
}

// Additional helper methods remain the same as in the original implementation...
// (setProcessingTimeout, RemoveFromProcessing, etc.)

func (s *QueueService) RemoveFromProcessing(ctx context.Context, eventID, userID string) error {
	processingKey := fmt.Sprintf("queue:processing:%s", eventID)

	members, err := s.Redis.SMembers(ctx, processingKey).Result()
	if err != nil {
		return err
	}

	for _, member := range members {
		var user models.ProcessingUser
		if err := json.Unmarshal([]byte(member), &user); err != nil {
			continue
		}

		if user.UserID == userID {
			s.Redis.SRem(ctx, processingKey, member)

			// todo: use share seatService instance
			seatService := NewSeatService(s.Redis)
			for _, seatID := range user.LockedSeats {
				seatService.UnlockSeat(ctx, eventID, seatID)
			}

			userKey := fmt.Sprintf("user:queue:%s:%s", eventID, userID)
			s.Redis.Del(ctx, userKey)

			break
		}
	}

	return nil
}

func (s *QueueService) setProcessingTimeout(ctx context.Context, eventID, userID string) {
	time.Sleep(s.Config.SeatLockTimeout)

	userKey := fmt.Sprintf("user:queue:%s:%s", eventID, userID)
	status, err := s.Redis.HGet(ctx, userKey, "status").Result()
	// todo: check seat status
	if err != nil || status != "processing" {
		// s.RemoveFromProcessing(ctx, eventID, userID)
		// go s.ProcessQueue(ctx, eventID)
		return
	}

	// time.Sleep(s.config.PaymentTimeout)
	// if err == nil || status != "payment_success" {
	// s.RemoveFromProcessing(ctx, eventID, userID)
	// go s.ProcessQueue(ctx, eventID)
	// return
	// }

	// todo: remove this line
	s.RemoveFromProcessing(ctx, eventID, userID)
	go s.ProcessQueue(ctx, eventID)
}
