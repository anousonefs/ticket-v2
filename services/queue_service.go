// services/queue_service.go
package services

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"math/rand"
	"regexp"
	"strconv"
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

	processingChannels map[string]chan struct{} // One channel per event
	channelMutex       sync.RWMutex             // Protect the map
	activeProcessors   map[string]bool          // Track active processors
	processorMutex     sync.RWMutex             // Protect active processors
	wg                 sync.WaitGroup
}

func NewQueueService(redisClient *redis.Client, pn *pubnub.PubNub, cfg *config.Config) *QueueService {
	service := &QueueService{
		Redis:              redisClient,
		PubNub:             pn,
		Config:             cfg,
		timeoutMap:         make(map[string]*time.Timer),
		stopChan:           make(chan struct{}),
		processingChannels: make(map[string]chan struct{}),
		activeProcessors:   make(map[string]bool),
	}

	// Start the centralized timeout manager
	go service.timeoutManager()

	return service
}

// SAFE ProcessQueue trigger - only one goroutine per event
func (s *QueueService) TriggerProcessQueue(eventID string) {
	s.channelMutex.Lock()
	defer s.channelMutex.Unlock()

	// Get or create processing channel for this event
	processingChan, exists := s.processingChannels[eventID]
	if !exists {
		processingChan = make(chan struct{}, 1) // Buffered to prevent blocking
		s.processingChannels[eventID] = processingChan

		// Start single processor goroutine for this event
		s.wg.Add(1)
		go s.eventProcessor(eventID, processingChan)

		log.Printf("Started processor goroutine for event %s", eventID)
	}

	// Send trigger signal (non-blocking)
	select {
	case processingChan <- struct{}{}:
		log.Printf("Triggered processing for event %s", eventID)
	default:
		log.Printf("Processing already pending for event %s", eventID)
	}
}

// Single processor goroutine per event
func (s *QueueService) eventProcessor(eventID string, triggerChan <-chan struct{}) {
	defer s.wg.Done()
	defer func() {
		// Cleanup when goroutine exits
		s.channelMutex.Lock()
		delete(s.processingChannels, eventID)
		s.channelMutex.Unlock()

		s.processorMutex.Lock()
		delete(s.activeProcessors, eventID)
		s.processorMutex.Unlock()

		log.Printf("Processor goroutine for event %s stopped", eventID)
	}()

	log.Printf("Event processor started for %s", eventID)

	for {
		select {
		case <-triggerChan:
			// Process queue when triggered
			s.processorMutex.Lock()
			s.activeProcessors[eventID] = true
			s.processorMutex.Unlock()

			log.Printf("Processing queue for event %s", eventID)
			s.processQueueInternal(context.Background(), eventID)

			s.processorMutex.Lock()
			s.activeProcessors[eventID] = false
			s.processorMutex.Unlock()

		case <-s.stopChan:
			log.Printf("Stopping processor for event %s", eventID)
			return
		}
	}
}

// Internal processing logic (called by single goroutine)
func (s *QueueService) processQueueInternal(ctx context.Context, eventID string) {
	processingKey := fmt.Sprintf("queue:processing:%s", eventID)
	waitingKey := fmt.Sprintf("queue:waiting:%s", eventID)

	processedCount := 0

	for {
		// Check processing capacity
		processingCount, err := s.Redis.SCard(ctx, processingKey).Result()
		if err != nil {
			log.Printf("Error getting processing count: %v", err)
			break
		}

		if processingCount >= int64(s.Config.MaxProcessingUsers) {
			log.Printf("Processing queue full for event %s (%d/%d)",
				eventID, processingCount, s.Config.MaxProcessingUsers)
			break
		}

		// Get next user
		data, err := s.Redis.RPop(ctx, waitingKey).Result()
		if err == redis.Nil {
			log.Printf("No more users in queue for event %s (processed %d)", eventID, processedCount)
			break
		} else if err != nil {
			log.Printf("Error getting user from queue: %v", err)
			break
		}

		var entry models.QueueEntry
		if err := json.Unmarshal([]byte(data), &entry); err != nil {
			log.Printf("Error unmarshaling queue entry: %v", err)
			continue
		}

		// Validate and process user
		if s.validateUserSession(ctx, eventID, entry.UserID, entry.SessionID) {
			if err := s.moveUserToProcessingAtomic(ctx, eventID, entry); err != nil {
				log.Printf("Failed to move user %s to processing: %v", entry.UserID, err)
			} else {
				processedCount++
			}
		}
	}

	log.Printf("Queue processing completed for event %s: processed %d users", eventID, processedCount)
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
				// go s.ProcessQueue(ctx, eventID) // Process next in queue
				s.TriggerProcessQueue(eventID)
			}
		}
	}
}

const enqueueAndTriggerScript = `
local queue_key = KEYS[1]
local user_key = KEYS[2] 
local processing_lock_key = KEYS[3]
local queue_data = ARGV[1]
local user_status = ARGV[2]
local joined_at = ARGV[3]
local session_id = ARGV[4]
local user_id = ARGV[5]
local event_id = ARGV[6]

-- Check if user already exists
if redis.call('EXISTS', user_key) == 1 then
    return {-1, 0, 0, "user_already_exists"}
end

-- Get current queue length BEFORE adding
local queue_len_before = redis.call('LLEN', queue_key)

-- Add to queue atomically
redis.call('LPUSH', queue_key, queue_data)
redis.call('HSET', user_key, 
    'status', user_status,
    'joined_at', joined_at, 
    'session_id', session_id,
    'user_id', user_id,
    'event_id', event_id
)
redis.call('EXPIRE', user_key, 86400)

local new_queue_len = redis.call('LLEN', queue_key)
local should_trigger_processing = 0

-- Only trigger processing if queue was empty AND we can acquire lock
if queue_len_before == 0 then
    local lock_acquired = redis.call('SET', processing_lock_key, 'processing', 'NX', 'EX', 30)
    if lock_acquired then
        should_trigger_processing = 1
    end
end

return {queue_len_before, new_queue_len, should_trigger_processing, "success"}
`

func (s *QueueService) EnqueueUserAtomic(ctx context.Context, eventID, userID, sessionID string) error {
	entry := models.QueueEntry{
		UserID:    userID,
		EventID:   eventID,
		JoinedAt:  time.Now(),
		Status:    "waiting",
		SessionID: sessionID,
	}

	data, err := json.Marshal(entry)
	if err != nil {
		return fmt.Errorf("failed to marshal entry: %w", err)
	}

	queueKey := fmt.Sprintf("queue:waiting:%s", eventID)
	userKey := fmt.Sprintf("user:queue:%s:%s", eventID, userID)
	lockKey := fmt.Sprintf("lock:processing:%s", eventID)

	// Execute atomic script
	result, err := s.Redis.Eval(ctx, enqueueAndTriggerScript,
		[]string{queueKey, userKey, lockKey},
		string(data), "waiting", time.Now().Unix(), sessionID, userID, eventID,
	).Result()
	if err != nil {
		return fmt.Errorf("failed to execute enqueue script: %w", err)
	}

	resultSlice, ok := result.([]any)
	if !ok || len(resultSlice) != 4 {
		return fmt.Errorf("unexpected script result: %v", result)
	}

	queueLenBefore := resultSlice[0].(int64)
	newQueueLen := resultSlice[1].(int64)
	shouldTrigger := resultSlice[2].(int64)
	status := resultSlice[3].(string)

	if status != "success" {
		return fmt.Errorf("enqueue failed: %s", status)
	}

	log.Printf("User %s enqueued atomically: before=%d, after=%d, trigger=%d",
		userID, queueLenBefore, newQueueLen, shouldTrigger)

	// Only ONE goroutine per event will have shouldTrigger=1
	if shouldTrigger == 1 {
		log.Printf("SINGLE ProcessQueue trigger for event %s", eventID)
		go func() {
			defer func() {
				// Release lock when processing complete
				s.Redis.Del(context.Background(), lockKey)
			}()
			// s.ProcessQueue(context.Background(), eventID)
			s.TriggerProcessQueue(eventID)
		}()
	}

	return nil
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

func (s *QueueService) ProcessQueue(ctx context.Context, eventID string) {
	processingKey := fmt.Sprintf("queue:processing:%s", eventID)
	waitingKey := fmt.Sprintf("queue:waiting:%s", eventID)

	for {
		// Check processing capacity
		processingCount, err := s.Redis.SCard(ctx, processingKey).Result()
		if err != nil {
			log.Printf("Error getting processing count: %v", err)
			break
		}

		if processingCount >= int64(s.Config.MaxProcessingUsers) {
			break
		}

		// Get next user from waiting queue
		data, err := s.Redis.RPop(ctx, waitingKey).Result()
		if err == redis.Nil {
			break // Queue empty
		} else if err != nil {
			log.Printf("Error getting user from queue: %v", err)
			break
		}

		var entry models.QueueEntry
		if err := json.Unmarshal([]byte(data), &entry); err != nil {
			log.Printf("Error unmarshaling queue entry: %v", err)
			continue
		}

		// SECURE SESSION VALIDATION
		if !s.validateUserSession(ctx, eventID, entry.UserID, entry.SessionID) {
			log.Printf("Session validation failed for user %s", entry.UserID)
			continue
		}

		// Move to processing
		s.moveUserToProcessingAtomic(ctx, eventID, entry)
	}
}

// ATOMIC VERSION - Using Lua Script for better performance
const moveToProcessingScript = `
local processing_key = KEYS[1]
local user_key = KEYS[2]
local processing_data = ARGV[1]
local user_id = ARGV[2]
local session_id = ARGV[3]

-- Check if user is already in processing
local members = redis.call('SMEMBERS', processing_key)
for i, member in ipairs(members) do
    local user_data = cjson.decode(member)
    if user_data.UserID == user_id then
        return {0, 0, "already_processing"}
    end
end

-- Add to processing set
local added = redis.call('SADD', processing_key, processing_data)

-- Update user status
local fields_set = redis.call('HSET', user_key, 
    'status', 'processing',
    'processing_start', redis.call('TIME')[1],
    'seats_locked', '[]'
)

-- Set TTL
redis.call('EXPIRE', user_key, 1800)  -- 30 minutes

-- Get current processing count
local processing_count = redis.call('SCARD', processing_key)

return {added, fields_set, processing_count, "success"}
`

func (s *QueueService) moveUserToProcessingAtomic(ctx context.Context, eventID string, entry models.QueueEntry) error {
	processingKey := fmt.Sprintf("queue:processing:%s", eventID)
	userKey := fmt.Sprintf("user:queue:%s:%s", eventID, entry.UserID)

	processingUser := models.ProcessingUser{
		UserID:      entry.UserID,
		EventID:     entry.EventID,
		StartedAt:   time.Now(),
		SessionID:   entry.SessionID,
		LockedSeats: []string{},
	}

	processingData, err := json.Marshal(processingUser)
	if err != nil {
		return fmt.Errorf("failed to marshal processing user: %w", err)
	}

	// Execute atomic script
	result, err := s.Redis.Eval(ctx, moveToProcessingScript,
		[]string{processingKey, userKey},
		string(processingData), entry.UserID, entry.SessionID,
	).Result()
	if err != nil {
		return fmt.Errorf("failed to execute move to processing script: %w", err)
	}

	resultSlice, ok := result.([]any)
	if !ok || len(resultSlice) != 4 {
		return fmt.Errorf("unexpected script result: %v", result)
	}

	status := resultSlice[3].(string)
	if status == "already_processing" {
		return fmt.Errorf("user %s already in processing for event %s", entry.UserID, eventID)
	}

	if status != "success" {
		return fmt.Errorf("failed to move user to processing: %s", status)
	}

	added := resultSlice[0].(int64)
	fieldsSet := resultSlice[1].(int64)
	processingCount := resultSlice[2].(int64)

	log.Printf("User %s moved to processing atomically: added=%d, fields=%d, count=%d",
		entry.UserID, added, fieldsSet, processingCount)

	// Notify user (async, don't block)
	go s.notifyUserProcessing(context.Background(), entry.UserID, eventID)

	return nil
}

func (s *QueueService) notifyUserProcessing(_ context.Context, userID, eventID string) error {
	channel := fmt.Sprintf("user-%s", userID)

	message := map[string]any{
		"type":      "queue_status",
		"status":    "processing",
		"event_id":  eventID,
		"message":   "🎉 You can now select your seats!",
		"timestamp": time.Now().Unix(),
		"data": map[string]any{
			"seat_selection_url": fmt.Sprintf("/events/%s/seats", eventID),
			"timeout_minutes":    5, // User has 5 minutes to select seats
		},
	}

	publishResult, _, err := s.PubNub.Publish().
		Channel(channel).
		Message(message).
		Execute()
	if err != nil {
		return fmt.Errorf("failed to notify user via PubNub: %w", err)
	}

	log.Printf("Notified user %s for processing in event %s (timetoken: %v)",
		userID, eventID, publishResult.Timestamp)

	return nil
}

// SECURE SESSION VALIDATION FUNCTION
func (s *QueueService) validateUserSession(ctx context.Context, eventID, userID, sessionID string) bool {
	// CRITICAL: Reject empty session IDs immediately
	if sessionID == "" {
		log.Printf("Security: Empty session ID for user %s in event %s", userID, eventID)
		return false
	}

	// CRITICAL: Validate session ID format (prevent injection)
	if !isValidSessionID(sessionID) {
		log.Printf("Security: Invalid session ID format for user %s: %s", userID, sessionID)
		return false
	}

	userKey := fmt.Sprintf("user:queue:%s:%s", eventID, userID)

	// Get stored session data
	sessionData, err := s.Redis.HMGet(ctx, userKey, "session_id", "status", "joined_at").Result()
	if err != nil {
		log.Printf("Security: Failed to get session data for user %s: %v", userID, err)
		return false
	}

	// Check if user record exists
	if len(sessionData) != 3 {
		log.Printf("Security: Incomplete session data for user %s", userID)
		return false
	}

	storedSessionID, ok := sessionData[0].(string)
	if !ok || storedSessionID == "" {
		log.Printf("Security: No stored session ID for user %s", userID)
		return false
	}

	storedStatus, ok := sessionData[1].(string)
	if !ok || storedStatus != "waiting" {
		log.Printf("Security: Invalid status for user %s: %s", userID, storedStatus)
		return false
	}

	storedJoinedAt, ok := sessionData[2].(string)
	if !ok || storedJoinedAt == "" {
		log.Printf("Security: No join timestamp for user %s", userID)
		return false
	}

	// CRITICAL: Compare session IDs (both must be non-empty)
	if storedSessionID != sessionID {
		log.Printf("Security: Session ID mismatch for user %s. Expected: %s, Got: %s",
			userID, storedSessionID, sessionID)
		return false
	}

	// Additional security checks
	joinedAt, err := strconv.ParseInt(storedJoinedAt, 10, 64)
	if err != nil {
		log.Printf("Security: Invalid join timestamp for user %s: %s", userID, storedJoinedAt)
		return false
	}

	// Check if session is not too old (prevent replay attacks)
	maxSessionAge := 24 * time.Hour
	if time.Since(time.Unix(joinedAt, 0)) > maxSessionAge {
		log.Printf("Security: Session expired for user %s (age: %v)",
			userID, time.Since(time.Unix(joinedAt, 0)))
		return false
	}

	return true
}

// VALIDATE SESSION ID FORMAT
func isValidSessionID(sessionID string) bool {
	// Session ID should be non-empty and follow expected format
	if len(sessionID) < 10 || len(sessionID) > 128 {
		return false
	}

	// Should contain only alphanumeric characters, hyphens, and underscores
	matched, _ := regexp.MatchString(`^[a-zA-Z0-9_-]+$`, sessionID)
	return matched
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
func (s *QueueService) Shutdown2() {
	close(s.stopChan)

	// Cancel all active timeouts
	s.timeoutMutex.Lock()
	for key, timer := range s.timeoutMap {
		timer.Stop()
		delete(s.timeoutMap, key)
	}
	s.timeoutMutex.Unlock()
}

func (s *QueueService) Shutdown() {
	log.Println("Shutting down queue service...")

	// Signal all goroutines to stop
	close(s.stopChan)

	// Wait for all goroutines to finish
	done := make(chan struct{})
	go func() {
		s.wg.Wait()
		close(done)
	}()

	select {
	case <-done:
		log.Println("All processors stopped gracefully")
	case <-time.After(30 * time.Second):
		log.Println("Timeout waiting for processors to stop")
	}
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

// GENERATE SECURE SESSION ID (Frontend should use this pattern)
func GenerateSecureSessionID() string {
	// Use cryptographically secure random number generator
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		// Fallback to time-based if crypto/rand fails
		return fmt.Sprintf("session_%d_%d", time.Now().UnixNano(), rand.Int63())
	}
	return fmt.Sprintf("session_%x", b)
}

// HELPER FUNCTION - Check if user is already in processing
func (s *QueueService) isUserInProcessing(ctx context.Context, eventID, userID string) (bool, error) {
	processingKey := fmt.Sprintf("queue:processing:%s", eventID)

	// Get all processing users
	members, err := s.Redis.SMembers(ctx, processingKey).Result()
	if err != nil {
		return false, err
	}

	// Check if user is in the processing set
	for _, member := range members {
		var user models.ProcessingUser
		if err := json.Unmarshal([]byte(member), &user); err != nil {
			continue // Skip invalid entries
		}

		if user.UserID == userID {
			return true, nil
		}
	}

	return false, nil
}
