package services

import (
	"context"
	"encoding/json"
	"testing"
	"ticket-system/config"
	"ticket-system/models"
	"time"

	"github.com/go-redis/redismock/v9"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func setupTestQueueService() (*QueueService, redismock.ClientMock) {
	db, mock := redismock.NewClientMock()
	cfg := &config.Config{
		ProcessingTimeout:     5 * time.Minute,
		SeatLockTimeout:      5 * time.Minute,
		MaxProcessingUsers:   10,
		QueuePositionUpdate:  2 * time.Second,
	}

	service := &QueueService{
		Redis:      db,
		PubNub:     nil, // Mock if needed
		Config:     cfg,
		timeoutMap: make(map[string]*time.Timer),
		stopChan:   make(chan struct{}),
	}

	return service, mock
}

func TestQueueService_EnqueueUserAtomic_Success(t *testing.T) {
	service, mock := setupTestQueueService()
	defer mock.ClearExpected()

	ctx := context.Background()
	eventID := "test-event"
	userID := "test-user"
	sessionID := "test-session"

	// Mock the Lua script execution
	scriptResult := []interface{}{int64(0), int64(1), int64(1), "success"}
	mock.ExpectEval(enqueueAndTriggerScript, []string{
		"queue:waiting:test-event",
		"user:queue:test-event:test-user",
		"lock:processing:test-event",
	}, 6).SetVal(scriptResult)

	// Mock the Redis.Del call for lock cleanup (from the goroutine)
	mock.ExpectDel("lock:processing:test-event").SetVal(1)

	err := service.EnqueueUserAtomic(ctx, eventID, userID, sessionID)

	assert.NoError(t, err)
	
	// Give some time for the goroutine to execute
	time.Sleep(100 * time.Millisecond)
	
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestQueueService_EnqueueUserAtomic_UserAlreadyExists(t *testing.T) {
	service, mock := setupTestQueueService()
	defer mock.ClearExpected()

	ctx := context.Background()
	eventID := "test-event"
	userID := "test-user"
	sessionID := "test-session"

	// Mock script returning user already exists
	scriptResult := []interface{}{int64(-1), int64(0), int64(0), "user_already_exists"}
	mock.ExpectEval(enqueueAndTriggerScript, []string{
		"queue:waiting:test-event",
		"user:queue:test-event:test-user",
		"lock:processing:test-event",
	}, 6).SetVal(scriptResult)

	err := service.EnqueueUserAtomic(ctx, eventID, userID, sessionID)

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "user_already_exists")
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestQueueService_ProcessQueue_Success(t *testing.T) {
	service, mock := setupTestQueueService()
	defer mock.ClearExpected()

	ctx := context.Background()
	eventID := "test-event"
	
	// Create test queue entry
	entry := models.QueueEntry{
		UserID:    "test-user",
		EventID:   eventID,
		JoinedAt:  time.Now(),
		Status:    "waiting",
		SessionID: "test-session",
	}
	entryData, _ := json.Marshal(entry)

	// Mock processing capacity check
	mock.ExpectSCard("queue:processing:test-event").SetVal(0)

	// Mock getting user from waiting queue
	mock.ExpectRPop("queue:waiting:test-event").SetVal(string(entryData))

	// Mock session validation
	mock.ExpectHMGet("user:queue:test-event:test-user", "session_id", "status", "joined_at").SetVal([]interface{}{
		"test-session",
		"waiting", 
		time.Now().Unix(),
	})

	// Mock the move to processing script
	scriptResult := []interface{}{int64(1), int64(3), int64(1), "success"}
	mock.ExpectEval(moveToProcessingScript, []string{
		"queue:processing:test-event",
		"user:queue:test-event:test-user",
	}, 3).SetVal(scriptResult)

	// Mock the second capacity check (queue empty)
	mock.ExpectSCard("queue:processing:test-event").SetVal(1)
	mock.ExpectRPop("queue:waiting:test-event").RedisNil()

	service.ProcessQueue(ctx, eventID)

	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestQueueService_ProcessQueue_CapacityFull(t *testing.T) {
	service, mock := setupTestQueueService()
	defer mock.ClearExpected()

	ctx := context.Background()
	eventID := "test-event"

	// Mock processing capacity is full
	mock.ExpectSCard("queue:processing:test-event").SetVal(int64(service.Config.MaxProcessingUsers))

	service.ProcessQueue(ctx, eventID)

	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestQueueService_ValidateUserSession_Valid(t *testing.T) {
	service, mock := setupTestQueueService()
	defer mock.ClearExpected()

	ctx := context.Background()
	eventID := "test-event"
	userID := "test-user"
	sessionID := "valid-session-123"

	// Mock session data retrieval
	mock.ExpectHMGet("user:queue:test-event:test-user", "session_id", "status", "joined_at").SetVal([]interface{}{
		sessionID,
		"waiting",
		time.Now().Unix(),
	})

	result := service.validateUserSession(ctx, eventID, userID, sessionID)

	assert.True(t, result)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestQueueService_ValidateUserSession_Invalid(t *testing.T) {
	service, mock := setupTestQueueService()
	defer mock.ClearExpected()

	ctx := context.Background()
	eventID := "test-event"
	userID := "test-user"
	sessionID := "invalid-session"

	// Mock session data retrieval with different session ID
	mock.ExpectHMGet("user:queue:test-event:test-user", "session_id", "status", "joined_at").SetVal([]interface{}{
		"different-session",
		"waiting",
		time.Now().Unix(),
	})

	result := service.validateUserSession(ctx, eventID, userID, sessionID)

	assert.False(t, result)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestQueueService_ValidateUserSession_EmptySessionID(t *testing.T) {
	service, _ := setupTestQueueService()

	ctx := context.Background()
	eventID := "test-event"
	userID := "test-user"
	sessionID := ""

	result := service.validateUserSession(ctx, eventID, userID, sessionID)

	assert.False(t, result)
}

func TestQueueService_ValidateUserSession_ExpiredSession(t *testing.T) {
	service, mock := setupTestQueueService()
	defer mock.ClearExpected()

	ctx := context.Background()
	eventID := "test-event"
	userID := "test-user"
	sessionID := "valid-session-123"

	// Mock session data with old timestamp (over 24 hours ago)
	oldTimestamp := time.Now().Add(-25 * time.Hour).Unix()
	mock.ExpectHMGet("user:queue:test-event:test-user", "session_id", "status", "joined_at").SetVal([]interface{}{
		sessionID,
		"waiting",
		oldTimestamp,
	})

	result := service.validateUserSession(ctx, eventID, userID, sessionID)

	assert.False(t, result)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestQueueService_RemoveFromProcessing_Success(t *testing.T) {
	service, mock := setupTestQueueService()
	defer mock.ClearExpected()

	ctx := context.Background()
	eventID := "test-event"
	userID := "test-user"

	// Create mock processing user
	processingUser := models.ProcessingUser{
		UserID:      userID,
		EventID:     eventID,
		StartedAt:   time.Now(),
		SessionID:   "test-session",
		LockedSeats: []string{"A1", "A2"},
	}
	userData, _ := json.Marshal(processingUser)

	// Mock getting processing users
	mock.ExpectSMembers("queue:processing:test-event").SetVal([]string{string(userData)})

	// Mock removing from processing set
	mock.ExpectSRem("queue:processing:test-event", string(userData)).SetVal(1)

	// Mock seat unlocking (simplified - actual implementation depends on SeatService)
	for _, seatID := range processingUser.LockedSeats {
		mock.ExpectDel("seat:lock:test-event:" + seatID).SetVal(1)
	}

	// Mock user cleanup
	mock.ExpectDel("user:queue:test-event:test-user").SetVal(1)

	err := service.RemoveFromProcessing(ctx, eventID, userID)

	assert.NoError(t, err)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestQueueService_GetQueueMetrics_Success(t *testing.T) {
	service, mock := setupTestQueueService()
	defer mock.ClearExpected()

	ctx := context.Background()
	eventID := "test-event"

	expectedMetrics := map[string]string{
		"total_in_queue": "10",
		"avg_wait_time":  "120.5",
		"last_updated":   "1640995200",
	}

	// Mock metrics retrieval
	mock.ExpectHGetAll("queue:metrics:test-event").SetVal(expectedMetrics)

	// Mock processing count
	mock.ExpectSCard("queue:processing:test-event").SetVal(3)

	metrics, err := service.GetQueueMetrics(ctx, eventID)

	assert.NoError(t, err)
	assert.Equal(t, expectedMetrics["total_in_queue"], metrics["total_in_queue"])
	assert.Equal(t, expectedMetrics["avg_wait_time"], metrics["avg_wait_time"])
	assert.Equal(t, int64(3), metrics["processing_count"])
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestQueueService_GetUserQueueStatus_Success(t *testing.T) {
	service, mock := setupTestQueueService()
	defer mock.ClearExpected()

	ctx := context.Background()
	eventID := "test-event"
	userID := "test-user"

	userStatus := map[string]string{
		"status":     "waiting",
		"session_id": "test-session",
		"joined_at":  time.Now().Unix(),
	}

	// Mock user status retrieval
	mock.ExpectHGetAll("user:queue:test-event:test-user").SetVal(userStatus)

	// Mock position retrieval for waiting user
	mock.ExpectGet("queue:position:test-event:test-user").SetVal("5")

	status, err := service.GetUserQueueStatus(ctx, eventID, userID)

	assert.NoError(t, err)
	assert.Equal(t, "waiting", status["status"])
	assert.Equal(t, "test-session", status["session_id"])
	assert.Equal(t, 5, status["position"])
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestQueueService_GetUserQueueStatus_NotInQueue(t *testing.T) {
	service, mock := setupTestQueueService()
	defer mock.ClearExpected()

	ctx := context.Background()
	eventID := "test-event"
	userID := "test-user"

	// Mock empty user status (user not in queue)
	mock.ExpectHGetAll("user:queue:test-event:test-user").SetVal(map[string]string{})

	status, err := service.GetUserQueueStatus(ctx, eventID, userID)

	assert.NoError(t, err)
	assert.Equal(t, "not_in_queue", status["status"])
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestIsValidSessionID(t *testing.T) {
	tests := []struct {
		name      string
		sessionID string
		expected  bool
	}{
		{"Valid session ID", "session_abc123-def456", true},
		{"Valid long session ID", "session_1234567890abcdef1234567890abcdef12345678", true},
		{"Empty session ID", "", false},
		{"Too short session ID", "short", false},
		{"Too long session ID", "this_is_a_very_long_session_id_that_exceeds_the_maximum_allowed_length_of_128_characters_and_should_be_rejected_by_validation", false},
		{"Invalid characters", "session@123#456", false},
		{"Valid with underscores", "session_123_456", true},
		{"Valid with hyphens", "session-123-456", true},
		{"Mixed valid characters", "session_123-abc_DEF-789", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := isValidSessionID(tt.sessionID)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestGenerateSecureSessionID(t *testing.T) {
	sessionID1 := GenerateSecureSessionID()
	sessionID2 := GenerateSecureSessionID()

	// Should generate different IDs
	assert.NotEqual(t, sessionID1, sessionID2)

	// Should be valid format
	assert.True(t, isValidSessionID(sessionID1))
	assert.True(t, isValidSessionID(sessionID2))

	// Should start with "session_"
	assert.Contains(t, sessionID1, "session_")
	assert.Contains(t, sessionID2, "session_")
}

func TestQueueService_UpdateEventQueuePositions(t *testing.T) {
	service, mock := setupTestQueueService()
	defer mock.ClearExpected()

	ctx := context.Background()
	eventID := "test-event"

	// Create test queue entries
	entries := []string{}
	for i := 1; i <= 3; i++ {
		entry := models.QueueEntry{
			UserID:   fmt.Sprintf("user-%d", i),
			EventID:  eventID,
			JoinedAt: time.Now().Add(-time.Duration(i) * time.Minute),
			Status:   "waiting",
		}
		entryData, _ := json.Marshal(entry)
		entries = append(entries, string(entryData))
	}

	// Mock queue entries retrieval
	mock.ExpectLRange("queue:waiting:test-event", 0, -1).SetVal(entries)

	// Mock position updates for each user
	for i, _ := range entries {
		posKey := fmt.Sprintf("queue:position:test-event:user-%d", i+1)
		mock.ExpectSet(posKey, i+1, 5*time.Second).SetVal("OK")
	}

	// Mock metrics update
	mock.ExpectHSet("queue:metrics:test-event", map[string]interface{}{
		"total_in_queue": 3,
		"avg_wait_time":  mock.Anything,
		"last_updated":   mock.Anything,
	}).SetVal(3)
	mock.ExpectExpire("queue:metrics:test-event", 24*time.Hour).SetVal(true)

	service.updateEventQueuePositions(ctx, eventID)

	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestQueueService_UpdateEventQueuePositions_EmptyQueue(t *testing.T) {
	service, mock := setupTestQueueService()
	defer mock.ClearExpected()

	ctx := context.Background()
	eventID := "test-event"

	// Mock empty queue
	mock.ExpectLRange("queue:waiting:test-event", 0, -1).SetVal([]string{})

	// Mock removal from active events
	mock.ExpectSRem("active_events", eventID).SetVal(1)
	mock.ExpectDel("queue:metrics:test-event").SetVal(1)

	service.updateEventQueuePositions(ctx, eventID)

	assert.NoError(t, mock.ExpectationsWereMet())
}

// Benchmark tests
func BenchmarkQueueService_EnqueueUserAtomic(b *testing.B) {
	service, mock := setupTestQueueService()
	defer mock.ClearExpected()

	ctx := context.Background()
	eventID := "bench-event"
	userID := "bench-user"
	sessionID := "bench-session"

	// Setup mock expectations for benchmark
	for i := 0; i < b.N; i++ {
		scriptResult := []interface{}{int64(0), int64(1), int64(0), "success"}
		mock.ExpectEval(enqueueAndTriggerScript, mock.Anything, mock.Anything).SetVal(scriptResult)
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		service.EnqueueUserAtomic(ctx, eventID, userID+fmt.Sprintf("-%d", i), sessionID)
	}
}

func BenchmarkQueueService_ValidateUserSession(b *testing.B) {
	service, mock := setupTestQueueService()
	defer mock.ClearExpected()

	ctx := context.Background()
	eventID := "bench-event"
	userID := "bench-user"
	sessionID := "bench-session-123"

	// Setup mock expectations for benchmark
	for i := 0; i < b.N; i++ {
		mock.ExpectHMGet(mock.Anything, "session_id", "status", "joined_at").SetVal([]interface{}{
			sessionID,
			"waiting",
			time.Now().Unix(),
		})
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		service.validateUserSession(ctx, eventID, userID, sessionID)
	}
}

// Integration-like test that tests multiple operations together
func TestQueueService_FullFlow_EnqueueProcessRemove(t *testing.T) {
	service, mock := setupTestQueueService()
	defer mock.ClearExpected()

	ctx := context.Background()
	eventID := "integration-event"
	userID := "integration-user"
	sessionID := "integration-session"

	// 1. Enqueue user
	scriptResult := []interface{}{int64(0), int64(1), int64(1), "success"}
	mock.ExpectEval(enqueueAndTriggerScript, []string{
		"queue:waiting:integration-event",
		"user:queue:integration-event:integration-user",
		"lock:processing:integration-event",
	}, 6).SetVal(scriptResult)
	mock.ExpectDel("lock:processing:integration-event").SetVal(1)

	err := service.EnqueueUserAtomic(ctx, eventID, userID, sessionID)
	require.NoError(t, err)

	// Give time for goroutine
	time.Sleep(50 * time.Millisecond)

	// 2. Get user status
	userStatus := map[string]string{
		"status":     "waiting",
		"session_id": sessionID,
		"joined_at":  fmt.Sprintf("%d", time.Now().Unix()),
	}
	mock.ExpectHGetAll("user:queue:integration-event:integration-user").SetVal(userStatus)
	mock.ExpectGet("queue:position:integration-event:integration-user").SetVal("1")

	status, err := service.GetUserQueueStatus(ctx, eventID, userID)
	require.NoError(t, err)
	assert.Equal(t, "waiting", status["status"])

	// 3. Remove from processing (simulating timeout)
	processingUser := models.ProcessingUser{
		UserID:      userID,
		EventID:     eventID,
		StartedAt:   time.Now(),
		SessionID:   sessionID,
		LockedSeats: []string{},
	}
	userData, _ := json.Marshal(processingUser)

	mock.ExpectSMembers("queue:processing:integration-event").SetVal([]string{string(userData)})
	mock.ExpectSRem("queue:processing:integration-event", string(userData)).SetVal(1)
	mock.ExpectDel("user:queue:integration-event:integration-user").SetVal(1)

	err = service.RemoveFromProcessing(ctx, eventID, userID)
	require.NoError(t, err)

	assert.NoError(t, mock.ExpectationsWereMet())
}