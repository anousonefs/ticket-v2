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
	Redis        *redis.Client
	PubNub       *pubnub.PubNub
	Config       *config.Config
	timeoutMap   map[string]*time.Timer // Track active timeouts
	timeoutMutex sync.RWMutex           // Protect timeout map
	stopChan     chan struct{}
}

func NewQueueService(redisClient *redis.Client, pn *pubnub.PubNub, cfg *config.Config) *QueueService {
	service := &QueueService{
		Redis:      redisClient,
		PubNub:     pn,
		Config:     cfg,
		timeoutMap: make(map[string]*time.Timer),
		stopChan:   make(chan struct{}),
	}

	// Start the centralized timeout manager
	go service.timeoutManager()

	return service
}

// UpdateQueuePositions - Improved version with batch updates and optimizations
func (s *QueueService) UpdateQueuePositions2(ctx context.Context) {
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
func (s *QueueService) EnqueueUser2(ctx context.Context, eventID, userID, sessionID string) error {
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
func (s *QueueService) ProcessQueue2(ctx context.Context, eventID string) {
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

func (s *QueueService) RemoveFromProcessing2(ctx context.Context, eventID, userID string) error {
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

// ---------------------

// Centralized timeout manager - only ONE goroutine for all timeouts
func (s *QueueService) timeoutManager() {
	ticker := time.NewTicker(10 * time.Second) // Check every 10 seconds
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			s.checkProcessingTimeouts()
		case <-s.stopChan:
			return
		}
	}
}

// Check for processing timeouts across all events
func (s *QueueService) checkProcessingTimeouts() {
	ctx := context.Background()

	// Get all processing queues
	keys, err := s.Redis.Keys(ctx, "queue:processing:*").Result()
	if err != nil {
		log.Printf("Error getting processing keys: %v", err)
		return
	}

	for _, key := range keys {
		eventID := key[len("queue:processing:"):]

		// Get all processing users
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
			if time.Since(user.StartedAt) > s.Config.SeatLockTimeout {
				log.Printf("Timeout detected for user %s in event %s", user.UserID, eventID)
				s.RemoveFromProcessing(ctx, eventID, user.UserID)
				go s.ProcessQueue(ctx, eventID) // Process next in queue
			}
		}
	}
}

// Add user to waiting queue
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
		return fmt.Errorf("failed to marshal queue entry: %w", err)
	}

	queueKey := fmt.Sprintf("queue:waiting:%s", eventID)
	userKey := fmt.Sprintf("user:queue:%s:%s", eventID, userID)

	// Use Redis transaction with proper command variable declarations
	pipe := s.Redis.TxPipeline()

	// Declare variables for each pipeline command
	queueLenCmd := pipe.LLen(ctx, queueKey)     // Get current length BEFORE adding
	lpushCmd := pipe.LPush(ctx, queueKey, data) // Add to front of queue
	hsetCmd := pipe.HSet(ctx, userKey, map[string]any{
		"status":     "waiting",
		"joined_at":  time.Now().Unix(),
		"session_id": sessionID,
	})

	// Execute all commands atomically
	_, err = pipe.Exec(ctx)
	if err != nil {
		return fmt.Errorf("failed to execute redis transaction: %w", err)
	}

	// Now safely get the results using the declared command variables
	queueLenBefore, err := queueLenCmd.Result()
	if err != nil {
		return fmt.Errorf("failed to get queue length: %w", err)
	}

	newQueueLen, err := lpushCmd.Result()
	if err != nil {
		return fmt.Errorf("failed to get new queue length: %w", err)
	}

	hsetResult, err := hsetCmd.Result()
	if err != nil {
		return fmt.Errorf("failed to set user status: %w", err)
	}

	log.Printf("==> User %s enqueued for event %s: queue_len_before=%d, new_queue_len=%d, hset_result=%d",
		userID, eventID, queueLenBefore, newQueueLen, hsetResult)

	// If queue was empty before adding, trigger processing
	if queueLenBefore == 0 {
		log.Printf("==> Queue was empty, triggering processing for event %s", eventID)
		go s.ProcessQueue(ctx, eventID)
	}

	return nil
}

// Process queue - move users from waiting to processing
func (s *QueueService) ProcessQueue(ctx context.Context, eventID string) {
	processingKey := fmt.Sprintf("queue:processing:%s", eventID)
	waitingKey := fmt.Sprintf("queue:waiting:%s", eventID)

	// Use a loop to process multiple users if slots are available
	for {
		// Check current processing count
		processingCount, err := s.Redis.SCard(ctx, processingKey).Result()
		if err != nil {
			log.Printf("Error getting processing count: %v", err)
			break
		}

		// Check if we can add more users to processing
		if processingCount >= int64(s.Config.MaxProcessingUsers) {
			log.Printf("Processing queue full for event %s (%d/%d)", eventID, processingCount, s.Config.MaxProcessingUsers)
			break
		}

		// Get next user from waiting queue (RPOP for FIFO)
		data, err := s.Redis.RPop(ctx, waitingKey).Result()
		if err == redis.Nil {
			// Queue is empty
			log.Printf("No more users in waiting queue for event %s", eventID)
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

		// Check if user session is still valid
		userKey := fmt.Sprintf("user:queue:%s:%s", eventID, entry.UserID)
		storedSessionID, err := s.Redis.HGet(ctx, userKey, "session_id").Result()
		if err != nil || storedSessionID != entry.SessionID {
			log.Printf("Invalid session for user %s, skipping", entry.UserID)
			continue
		}

		// Move to processing
		processingUser := models.ProcessingUser{
			UserID:    entry.UserID,
			EventID:   entry.EventID,
			StartedAt: time.Now(),
			SessionID: entry.SessionID,
		}

		processingData, _ := json.Marshal(processingUser)
		err = s.Redis.SAdd(ctx, processingKey, processingData).Err()
		if err != nil {
			log.Printf("Error adding user to processing: %v", err)
			// Put user back to front of waiting queue
			s.Redis.RPush(ctx, waitingKey, data)
			continue
		}

		// Update user status
		s.Redis.HSet(ctx, userKey, "status", "processing")

		// Notify user via PubNub
		channel := fmt.Sprintf("user-%s", entry.UserID)
		s.PubNub.Publish().
			Channel(channel).
			Message(map[string]any{
				"type":     "queue_status",
				"status":   "processing",
				"event_id": eventID,
				"message":  "You can now select your seats!",
			}).
			Execute()

		log.Printf("User %s moved to processing for event %s", entry.UserID, eventID)
	}
}

// Remove user from processing
func (s *QueueService) RemoveFromProcessing(ctx context.Context, eventID, userID string) error {
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
			s.PubNub.Publish().
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

// Update queue positions - runs as background task
func (s *QueueService) UpdateQueuePositions(ctx context.Context) {
	ticker := time.NewTicker(s.Config.QueuePositionUpdate)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			s.updatePositions(ctx)
		case <-s.stopChan:
			return
		}
	}
}

func (s *QueueService) updatePositions(ctx context.Context) {
	// Get all event queues
	keys, err := s.Redis.Keys(ctx, "queue:waiting:*").Result()
	if err != nil {
		log.Printf("Error getting queue keys: %v", err)
		return
	}

	for _, key := range keys {
		eventID := key[len("queue:waiting:"):]

		// Get all queue entries (FIFO order)
		entries, err := s.Redis.LRange(ctx, key, 0, -1).Result()
		if err != nil {
			continue
		}

		// Update positions and notify users
		for i, entryData := range entries {
			var entry models.QueueEntry
			if err := json.Unmarshal([]byte(entryData), &entry); err != nil {
				continue
			}

			// Position is from the end (FIFO)
			position := len(entries) - i

			// Store position in Redis with TTL
			posKey := fmt.Sprintf("queue:position:%s:%s", eventID, entry.UserID)
			s.Redis.Set(ctx, posKey, position, 10*time.Second)

			// Only notify if position changed significantly or periodically
			if position <= 50 || position%10 == 0 {
				channel := fmt.Sprintf("user-%s", entry.UserID)
				s.PubNub.Publish().
					Channel(channel).
					Message(map[string]any{
						"type":     "queue_position",
						"position": position,
						"event_id": eventID,
						"message":  fmt.Sprintf("You are #%d in line", position),
					}).
					Execute()
			}
		}
	}
}

// Get user's current queue status
func (s *QueueService) GetUserQueueStatus(ctx context.Context, eventID, userID string) (map[string]any, error) {
	userKey := fmt.Sprintf("user:queue:%s:%s", eventID, userID)

	status, err := s.Redis.HGetAll(ctx, userKey).Result()
	if err != nil {
		return nil, err
	}

	if len(status) == 0 {
		return map[string]any{
			"status": "not_in_queue",
		}, nil
	}

	result := map[string]any{
		"status":     status["status"],
		"session_id": status["session_id"],
	}

	// Get position if waiting
	if status["status"] == "waiting" {
		posKey := fmt.Sprintf("queue:position:%s:%s", eventID, userID)
		position, err := s.Redis.Get(ctx, posKey).Int()
		if err == nil {
			result["position"] = position
		}
	}

	return result, nil
}

// Graceful shutdown
func (s *QueueService) Shutdown() {
	close(s.stopChan)

	// Cancel all active timeouts
	s.timeoutMutex.Lock()
	for key, timer := range s.timeoutMap {
		timer.Stop()
		delete(s.timeoutMap, key)
	}
	s.timeoutMutex.Unlock()
}

// Health check for queue service
func (s *QueueService) HealthCheck(ctx context.Context) map[string]any {
	stats := map[string]any{
		"service": "queue",
		"status":  "healthy",
	}

	// Get queue statistics
	keys, _ := s.Redis.Keys(ctx, "queue:waiting:*").Result()
	totalWaiting := 0
	totalProcessing := 0

	for _, key := range keys {
		eventID := key[len("queue:waiting:"):]

		waiting, _ := s.Redis.LLen(ctx, key).Result()
		totalWaiting += int(waiting)

		processingKey := fmt.Sprintf("queue:processing:%s", eventID)
		processing, _ := s.Redis.SCard(ctx, processingKey).Result()
		totalProcessing += int(processing)
	}

	stats["active_events"] = len(keys)
	stats["total_waiting"] = totalWaiting
	stats["total_processing"] = totalProcessing

	return stats
}
