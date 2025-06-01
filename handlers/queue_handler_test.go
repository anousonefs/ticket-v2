package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/labstack/echo/v5"
	"github.com/pocketbase/pocketbase"
	"github.com/pocketbase/pocketbase/apis"
	"github.com/pocketbase/pocketbase/models"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Mock Redis Client for testing
type MockRedisClient struct {
	mock.Mock
	data map[string]interface{}
}

func (m *MockRedisClient) Exists(ctx context.Context, keys ...string) *mock.Call {
	args := []interface{}{ctx}
	for _, key := range keys {
		args = append(args, key)
	}
	return m.Called(args...)
}

func (m *MockRedisClient) Get(ctx context.Context, key string) *mock.Call {
	return m.Called(ctx, key)
}

func (m *MockRedisClient) HGet(ctx context.Context, key, field string) *mock.Call {
	return m.Called(ctx, key, field)
}

func (m *MockRedisClient) HGetAll(ctx context.Context, key string) *mock.Call {
	return m.Called(ctx, key)
}

func (m *MockRedisClient) Set(ctx context.Context, key string, value interface{}, expiration time.Duration) *mock.Call {
	return m.Called(ctx, key, value, expiration)
}

func (m *MockRedisClient) Del(ctx context.Context, keys ...string) *mock.Call {
	args := []interface{}{ctx}
	for _, key := range keys {
		args = append(args, key)
	}
	return m.Called(args...)
}

// Mock Redis Command Results
type MockIntResult struct {
	val int64
	err error
}

func (m *MockIntResult) Result() (int64, error) {
	return m.val, m.err
}

func (m *MockIntResult) Int() (int, error) {
	return int(m.val), m.err
}

type MockStringResult struct {
	val string
	err error
}

func (m *MockStringResult) Result() (string, error) {
	return m.val, m.err
}

type MockMapStringStringResult struct {
	val map[string]string
	err error
}

func (m *MockMapStringStringResult) Result() (map[string]string, error) {
	return m.val, m.err
}

// Mock QueueService for handler testing
type MockQueueService struct {
	mock.Mock
	Redis *MockRedisClient
}

func (m *MockQueueService) EnqueueUserAtomic(ctx context.Context, eventID, userID, sessionID string) error {
	args := m.Called(ctx, eventID, userID, sessionID)
	return args.Error(0)
}

func (m *MockQueueService) GetQueueMetrics(ctx context.Context, eventID string) (map[string]any, error) {
	args := m.Called(ctx, eventID)
	return args.Get(0).(map[string]any), args.Error(1)
}

func (m *MockQueueService) RemoveFromProcessing(ctx context.Context, eventID, userID string) error {
	args := m.Called(ctx, eventID, userID)
	return args.Error(0)
}

func setupTestApp() *pocketbase.PocketBase {
	app := pocketbase.New()
	// Configure test mode settings here if needed
	return app
}

func setupTestHandler() (*QueueHandler, *MockQueueService, *MockRedisClient) {
	app := setupTestApp()
	mockRedis := &MockRedisClient{
		data: make(map[string]interface{}),
	}
	mockQueueService := &MockQueueService{
		Redis: mockRedis,
	}

	handler := &QueueHandler{
		app:          app,
		queueService: mockQueueService,
	}

	return handler, mockQueueService, mockRedis
}

func createAuthenticatedContext(userID string) echo.Context {
	e := echo.New()
	req := httptest.NewRequest(http.MethodPost, "/", nil)
	rec := httptest.NewRecorder()
	c := e.NewContext(req, rec)

	// Mock authenticated user record
	authRecord := &models.Record{}
	authRecord.Id = userID
	c.Set(apis.ContextAuthRecordKey, authRecord)

	return c
}

func TestQueueHandler_EnterQueue_Success(t *testing.T) {
	handler, mockQueueService, mockRedis := setupTestHandler()

	// Test data
	userID := "test-user-123"
	eventID := "test-event-456"
	sessionID := "test-session-789"

	// Setup request
	requestBody := map[string]string{
		"event_id":   eventID,
		"session_id": sessionID,
	}
	bodyBytes, _ := json.Marshal(requestBody)

	req := httptest.NewRequest(http.MethodPost, "/api/queue/enter", bytes.NewReader(bodyBytes))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	e := echo.New()
	c := e.NewContext(req, rec)

	// Mock authenticated user
	authRecord := &models.Record{}
	authRecord.Id = userID
	c.Set(apis.ContextAuthRecordKey, authRecord)

	// Setup mocks
	mockRedis.On("Exists", mock.Anything, fmt.Sprintf("user:queue:%s:%s", eventID, userID)).Return(&MockIntResult{val: 0, err: nil})
	mockQueueService.On("EnqueueUserAtomic", mock.Anything, eventID, userID, sessionID).Return(nil)

	// Execute
	err := handler.EnterQueue(c)

	// Assert
	assert.NoError(t, err)
	assert.Equal(t, http.StatusOK, rec.Code)

	var response map[string]interface{}
	err = json.Unmarshal(rec.Body.Bytes(), &response)
	assert.NoError(t, err)
	assert.Equal(t, "Successfully joined queue", response["message"])
	assert.Equal(t, userID, response["user_id"])

	mockRedis.AssertExpectations(t)
	mockQueueService.AssertExpectations(t)
}

func TestQueueHandler_EnterQueue_Unauthorized(t *testing.T) {
	handler, _, _ := setupTestHandler()

	req := httptest.NewRequest(http.MethodPost, "/api/queue/enter", nil)
	rec := httptest.NewRecorder()

	e := echo.New()
	c := e.NewContext(req, rec)
	// No authenticated user set

	err := handler.EnterQueue(c)

	// Should return unauthorized error
	assert.Error(t, err)
	// Check if it's the expected unauthorized error type
	// Note: apis.NewUnauthorizedError returns an error that can be checked
}

func TestQueueHandler_EnterQueue_AlreadyInQueue(t *testing.T) {
	handler, _, mockRedis := setupTestHandler()

	userID := "test-user-123"
	eventID := "test-event-456"
	sessionID := "test-session-789"

	requestBody := map[string]string{
		"event_id":   eventID,
		"session_id": sessionID,
	}
	bodyBytes, _ := json.Marshal(requestBody)

	req := httptest.NewRequest(http.MethodPost, "/api/queue/enter", bytes.NewReader(bodyBytes))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	e := echo.New()
	c := e.NewContext(req, rec)

	authRecord := &models.Record{}
	authRecord.Id = userID
	c.Set(apis.ContextAuthRecordKey, authRecord)

	// Mock user already exists in queue
	mockRedis.On("Exists", mock.Anything, fmt.Sprintf("user:queue:%s:%s", eventID, userID)).Return(&MockIntResult{val: 1, err: nil})

	err := handler.EnterQueue(c)

	assert.Error(t, err)
	mockRedis.AssertExpectations(t)
}

func TestQueueHandler_GetQueuePosition_Success(t *testing.T) {
	handler, mockQueueService, mockRedis := setupTestHandler()

	userID := "test-user-123"
	eventID := "test-event-456"

	req := httptest.NewRequest(http.MethodGet, "/api/queue/position?event_id="+eventID, nil)
	rec := httptest.NewRecorder()

	e := echo.New()
	c := e.NewContext(req, rec)

	authRecord := &models.Record{}
	authRecord.Id = userID
	c.Set(apis.ContextAuthRecordKey, authRecord)

	// Setup mocks
	posKey := fmt.Sprintf("queue:position:%s:%s", eventID, userID)
	userKey := fmt.Sprintf("user:queue:%s:%s", eventID, userID)

	mockRedis.On("Get", mock.Anything, posKey).Return(&MockStringResult{val: "5", err: nil})
	mockRedis.On("HGet", mock.Anything, userKey, "status").Return(&MockStringResult{val: "waiting", err: nil})

	metrics := map[string]any{
		"total_in_queue": "10",
		"avg_wait_time":  "120",
	}
	mockQueueService.On("GetQueueMetrics", mock.Anything, eventID).Return(metrics, nil)

	err := handler.GetQueuePosition(c)

	assert.NoError(t, err)
	assert.Equal(t, http.StatusOK, rec.Code)

	var response map[string]interface{}
	err = json.Unmarshal(rec.Body.Bytes(), &response)
	assert.NoError(t, err)
	assert.Equal(t, float64(5), response["position"])
	assert.Equal(t, "waiting", response["status"])
	assert.Equal(t, "10", response["total_in_queue"])
	assert.Equal(t, "120", response["avg_wait_time"])

	mockRedis.AssertExpectations(t)
	mockQueueService.AssertExpectations(t)
}

func TestQueueHandler_GetQueuePosition_MissingEventID(t *testing.T) {
	handler, _, _ := setupTestHandler()

	userID := "test-user-123"

	req := httptest.NewRequest(http.MethodGet, "/api/queue/position", nil)
	rec := httptest.NewRecorder()

	e := echo.New()
	c := e.NewContext(req, rec)

	authRecord := &models.Record{}
	authRecord.Id = userID
	c.Set(apis.ContextAuthRecordKey, authRecord)

	err := handler.GetQueuePosition(c)

	assert.Error(t, err)
}

func TestQueueHandler_GetQueueMetrics_Success(t *testing.T) {
	handler, mockQueueService, _ := setupTestHandler()

	eventID := "test-event-456"

	req := httptest.NewRequest(http.MethodGet, "/api/queue/metrics?event_id="+eventID, nil)
	rec := httptest.NewRecorder()

	e := echo.New()
	c := e.NewContext(req, rec)

	expectedMetrics := map[string]any{
		"total_in_queue":    "10",
		"processing_count":  "3",
		"avg_wait_time":     "120",
		"last_updated":      "1640995200",
	}

	mockQueueService.On("GetQueueMetrics", mock.Anything, eventID).Return(expectedMetrics, nil)

	err := handler.GetQueueMetrics(c)

	assert.NoError(t, err)
	assert.Equal(t, http.StatusOK, rec.Code)

	var response map[string]interface{}
	err = json.Unmarshal(rec.Body.Bytes(), &response)
	assert.NoError(t, err)
	assert.Equal(t, expectedMetrics["total_in_queue"], response["total_in_queue"])
	assert.Equal(t, expectedMetrics["processing_count"], response["processing_count"])

	mockQueueService.AssertExpectations(t)
}

func TestQueueHandler_LeaveQueue_Success_ProcessingUser(t *testing.T) {
	handler, mockQueueService, mockRedis := setupTestHandler()

	userID := "test-user-123"
	eventID := "test-event-456"

	requestBody := map[string]string{
		"event_id": eventID,
	}
	bodyBytes, _ := json.Marshal(requestBody)

	req := httptest.NewRequest(http.MethodPost, "/api/queue/leave", bytes.NewReader(bodyBytes))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	e := echo.New()
	c := e.NewContext(req, rec)

	authRecord := &models.Record{}
	authRecord.Id = userID
	c.Set(apis.ContextAuthRecordKey, authRecord)

	userKey := fmt.Sprintf("user:queue:%s:%s", eventID, userID)

	// Mock user is in processing status
	mockRedis.On("HGet", mock.Anything, userKey, "status").Return(&MockStringResult{val: "processing", err: nil})
	mockQueueService.On("RemoveFromProcessing", mock.Anything, eventID, userID).Return(nil)

	err := handler.LeaveQueue(c)

	assert.NoError(t, err)
	assert.Equal(t, http.StatusOK, rec.Code)

	var response map[string]interface{}
	err = json.Unmarshal(rec.Body.Bytes(), &response)
	assert.NoError(t, err)
	assert.Equal(t, "Successfully left queue", response["message"])

	mockRedis.AssertExpectations(t)
	mockQueueService.AssertExpectations(t)
}

func TestQueueHandler_LeaveQueue_Success_WaitingUser(t *testing.T) {
	handler, _, mockRedis := setupTestHandler()

	userID := "test-user-123"
	eventID := "test-event-456"

	requestBody := map[string]string{
		"event_id": eventID,
	}
	bodyBytes, _ := json.Marshal(requestBody)

	req := httptest.NewRequest(http.MethodPost, "/api/queue/leave", bytes.NewReader(bodyBytes))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	e := echo.New()
	c := e.NewContext(req, rec)

	authRecord := &models.Record{}
	authRecord.Id = userID
	c.Set(apis.ContextAuthRecordKey, authRecord)

	userKey := fmt.Sprintf("user:queue:%s:%s", eventID, userID)

	// Mock user is in waiting status
	mockRedis.On("HGet", mock.Anything, userKey, "status").Return(&MockStringResult{val: "waiting", err: nil})
	mockRedis.On("Del", mock.Anything, userKey).Return(&MockIntResult{val: 1, err: nil})

	err := handler.LeaveQueue(c)

	assert.NoError(t, err)
	assert.Equal(t, http.StatusOK, rec.Code)

	var response map[string]interface{}
	err = json.Unmarshal(rec.Body.Bytes(), &response)
	assert.NoError(t, err)
	assert.Equal(t, "Successfully left queue", response["message"])

	mockRedis.AssertExpectations(t)
}

// Benchmark tests
func BenchmarkQueueHandler_EnterQueue(b *testing.B) {
	handler, mockQueueService, mockRedis := setupTestHandler()

	userID := "bench-user"
	eventID := "bench-event"
	sessionID := "bench-session"

	requestBody := map[string]string{
		"event_id":   eventID,
		"session_id": sessionID,
	}
	bodyBytes, _ := json.Marshal(requestBody)

	// Setup mocks
	mockRedis.On("Exists", mock.Anything, mock.AnythingOfType("string")).Return(&MockIntResult{val: 0, err: nil})
	mockQueueService.On("EnqueueUserAtomic", mock.Anything, eventID, userID, sessionID).Return(nil)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		req := httptest.NewRequest(http.MethodPost, "/api/queue/enter", bytes.NewReader(bodyBytes))
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()

		e := echo.New()
		c := e.NewContext(req, rec)

		authRecord := &models.Record{}
		authRecord.Id = userID
		c.Set(apis.ContextAuthRecordKey, authRecord)

		handler.EnterQueue(c)
	}
}

func BenchmarkQueueHandler_GetQueuePosition(b *testing.B) {
	handler, mockQueueService, mockRedis := setupTestHandler()

	userID := "bench-user"
	eventID := "bench-event"

	// Setup mocks
	mockRedis.On("Get", mock.Anything, mock.AnythingOfType("string")).Return(&MockStringResult{val: "5", err: nil})
	mockRedis.On("HGet", mock.Anything, mock.AnythingOfType("string"), "status").Return(&MockStringResult{val: "waiting", err: nil})
	mockQueueService.On("GetQueueMetrics", mock.Anything, eventID).Return(map[string]any{"total_in_queue": "10"}, nil)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		req := httptest.NewRequest(http.MethodGet, "/api/queue/position?event_id="+eventID, nil)
		rec := httptest.NewRecorder()

		e := echo.New()
		c := e.NewContext(req, rec)

		authRecord := &models.Record{}
		authRecord.Id = userID
		c.Set(apis.ContextAuthRecordKey, authRecord)

		handler.GetQueuePosition(c)
	}
}