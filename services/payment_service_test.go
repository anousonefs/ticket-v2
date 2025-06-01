package services

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/go-redis/redismock/v9"
	pubnub "github.com/pubnub/go"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

// Mock QueueService for PaymentService tests
type MockQueueServiceForPayment struct {
	mock.Mock
}

func (m *MockQueueServiceForPayment) RemoveFromProcessing(ctx context.Context, eventID, userID string) error {
	args := m.Called(ctx, eventID, userID)
	return args.Error(0)
}

func (m *MockQueueServiceForPayment) ProcessQueue(ctx context.Context, eventID string) {
	m.Called(ctx, eventID)
}

// Mock PubNub for testing
type MockPubNub struct {
	mock.Mock
	publishCalls []map[string]interface{}
}

func (m *MockPubNub) Publish() *MockPublishBuilder {
	return &MockPublishBuilder{
		pubnub: m,
	}
}

func (m *MockPubNub) AddListener(listener *pubnub.Listener) {
	m.Called(listener)
}

func (m *MockPubNub) Subscribe() *MockSubscribeBuilder {
	return &MockSubscribeBuilder{
		pubnub: m,
	}
}

type MockPublishBuilder struct {
	pubnub  *MockPubNub
	channel string
	message interface{}
}

func (m *MockPublishBuilder) Channel(channel string) *MockPublishBuilder {
	m.channel = channel
	return m
}

func (m *MockPublishBuilder) Message(message interface{}) *MockPublishBuilder {
	m.message = message
	return m
}

func (m *MockPublishBuilder) Execute() (*pubnub.PNPublishResponse, int, error) {
	m.pubnub.publishCalls = append(m.pubnub.publishCalls, map[string]interface{}{
		"channel": m.channel,
		"message": m.message,
	})
	
	args := m.pubnub.Called(m.channel, m.message)
	if len(args) > 0 {
		if resp, ok := args.Get(0).(*pubnub.PNPublishResponse); ok {
			return resp, args.Int(1), args.Error(2)
		}
	}
	return &pubnub.PNPublishResponse{}, 200, nil
}

type MockSubscribeBuilder struct {
	pubnub   *MockPubNub
	channels []string
}

func (m *MockSubscribeBuilder) Channels(channels []string) *MockSubscribeBuilder {
	m.channels = channels
	return m
}

func (m *MockSubscribeBuilder) Execute() {
	m.pubnub.Called(m.channels)
}

func setupTestPaymentService() (*PaymentService, redismock.ClientMock, *MockQueueServiceForPayment, *MockPubNub) {
	db, redisMock := redismock.NewClientMock()
	mockQueue := &MockQueueServiceForPayment{}
	mockPubNub := &MockPubNub{}

	// Create service without starting the subscription goroutine
	service := &PaymentService{
		Redis:  db,
		PubNub: mockPubNub,
		queue:  mockQueue,
	}

	return service, redisMock, mockQueue, mockPubNub
}

func TestPaymentService_CreatePaymentSession_Success(t *testing.T) {
	service, redisMock, _, _ := setupTestPaymentService()
	defer redisMock.ClearExpected()

	ctx := context.Background()
	userID := "test-user"
	eventID := "test-event"
	seats := []string{"A1", "A2"}
	amount := 100.50

	// Mock Redis operations
	redisMock.ExpectHSet("payment:payment_test-user_*", map[string]interface{}{
		"payment_id": mock.MatchedBy(func(v interface{}) bool {
			return v.(string) != ""
		}),
		"user_id":    userID,
		"event_id":   eventID,
		"seats":      seats,
		"amount":     amount,
		"status":     "pending",
		"created_at": mock.MatchedBy(func(v interface{}) bool {
			return v.(int64) > 0
		}),
	}).SetVal(7) // 7 fields set

	redisMock.ExpectExpire("payment:payment_test-user_*", 5*time.Minute).SetVal(true)

	paymentID, err := service.CreatePaymentSession(ctx, userID, eventID, seats, amount)

	assert.NoError(t, err)
	assert.NotEmpty(t, paymentID)
	assert.Contains(t, paymentID, "payment_test-user_")
	
	// Allow some flexibility in Redis mock matching
	time.Sleep(10 * time.Millisecond)
	
	// Verify the mock was called correctly (with some tolerance for timestamp)
	require.NoError(t, redisMock.ExpectationsWereMet())
}

func TestPaymentService_HandlePaymentNotification_Success(t *testing.T) {
	service, redisMock, mockQueue, mockPubNub := setupTestPaymentService()
	defer redisMock.ClearExpected()

	ctx := context.Background()
	paymentID := "payment_test-user_123"
	userID := "test-user"
	eventID := "test-event"
	seats := []string{"A1", "A2"}

	// Create mock message
	messageData := map[string]interface{}{
		"payment_id": paymentID,
		"status":     "success",
	}

	message := &pubnub.PNMessage{
		Message: messageData,
	}

	// Mock payment data retrieval
	paymentData := map[string]string{
		"payment_id": paymentID,
		"user_id":    userID,
		"event_id":   eventID,
		"seats":      `["A1","A2"]`,
		"amount":     "100.50",
		"status":     "pending",
	}

	redisMock.ExpectHGetAll("payment:" + paymentID).SetVal(paymentData)

	// Mock seat marking as sold for each seat
	for _, seatID := range seats {
		seatKey := fmt.Sprintf("seat:%s:%s", eventID, seatID)
		redisMock.ExpectHSet(seatKey, map[string]interface{}{
			"status":     "sold",
			"user_id":    userID,
			"locked_at":  mock.Anything,
			"sold_at":    mock.Anything,
		}).SetVal(4)
	}

	// Mock payment status update
	redisMock.ExpectHSet("payment:"+paymentID, "status", "completed").SetVal(1)

	// Mock queue operations
	mockQueue.On("RemoveFromProcessing", ctx, eventID, userID).Return(nil)
	mockQueue.On("ProcessQueue", ctx, eventID).Return()

	// Mock PubNub publish
	expectedMessage := map[string]interface{}{
		"type":       "payment_success",
		"payment_id": paymentID,
		"seats":      seats,
	}
	mockPubNub.On("Called", "user-"+userID, expectedMessage).Return(&pubnub.PNPublishResponse{}, 200, nil)

	// Execute
	service.handlePaymentNotification(message)

	// Give some time for async operations
	time.Sleep(100 * time.Millisecond)

	// Verify
	assert.NoError(t, redisMock.ExpectationsWereMet())
	mockQueue.AssertExpectations(t)
	
	// Check if PubNub publish was called
	assert.Len(t, mockPubNub.publishCalls, 1)
	publishCall := mockPubNub.publishCalls[0]
	assert.Equal(t, "user-"+userID, publishCall["channel"])
	
	publishedMessage := publishCall["message"].(map[string]interface{})
	assert.Equal(t, "payment_success", publishedMessage["type"])
	assert.Equal(t, paymentID, publishedMessage["payment_id"])
}

func TestPaymentService_HandlePaymentNotification_InvalidMessage(t *testing.T) {
	service, redisMock, _, _ := setupTestPaymentService()
	defer redisMock.ClearExpected()

	// Create invalid message (not a map)
	message := &pubnub.PNMessage{
		Message: "invalid message format",
	}

	// Execute - should handle gracefully
	service.handlePaymentNotification(message)

	// Should not make any Redis calls
	assert.NoError(t, redisMock.ExpectationsWereMet())
}

func TestPaymentService_HandlePaymentNotification_MissingPaymentData(t *testing.T) {
	service, redisMock, _, _ := setupTestPaymentService()
	defer redisMock.ClearExpected()

	paymentID := "payment_nonexistent"

	messageData := map[string]interface{}{
		"payment_id": paymentID,
		"status":     "success",
	}

	message := &pubnub.PNMessage{
		Message: messageData,
	}

	// Mock empty payment data (payment not found)
	redisMock.ExpectHGetAll("payment:" + paymentID).SetVal(map[string]string{})

	// Execute
	service.handlePaymentNotification(message)

	// Should handle gracefully
	assert.NoError(t, redisMock.ExpectationsWereMet())
}

func TestPaymentService_HandlePaymentNotification_NonSuccessStatus(t *testing.T) {
	service, redisMock, _, _ := setupTestPaymentService()
	defer redisMock.ClearExpected()

	messageData := map[string]interface{}{
		"payment_id": "payment_test_123",
		"status":     "failed",
	}

	message := &pubnub.PNMessage{
		Message: messageData,
	}

	// Execute - should not process failed payments
	service.handlePaymentNotification(message)

	// Should not make any Redis calls
	assert.NoError(t, redisMock.ExpectationsWereMet())
}

func TestPaymentService_HandlePaymentNotification_InvalidSeatsJSON(t *testing.T) {
	service, redisMock, mockQueue, _ := setupTestPaymentService()
	defer redisMock.ClearExpected()

	paymentID := "payment_test-user_123"
	userID := "test-user"
	eventID := "test-event"

	messageData := map[string]interface{}{
		"payment_id": paymentID,
		"status":     "success",
	}

	message := &pubnub.PNMessage{
		Message: messageData,
	}

	// Mock payment data with invalid seats JSON
	paymentData := map[string]string{
		"payment_id": paymentID,
		"user_id":    userID,
		"event_id":   eventID,
		"seats":      "invalid json",
		"amount":     "100.50",
		"status":     "pending",
	}

	redisMock.ExpectHGetAll("payment:" + paymentID).SetVal(paymentData)

	// Mock payment status update (should still happen)
	redisMock.ExpectHSet("payment:"+paymentID, "status", "completed").SetVal(1)

	// Mock queue operations (should still happen)
	mockQueue.On("RemoveFromProcessing", mock.Anything, eventID, userID).Return(nil)
	mockQueue.On("ProcessQueue", mock.Anything, eventID).Return()

	// Execute
	service.handlePaymentNotification(message)

	// Give some time for async operations
	time.Sleep(50 * time.Millisecond)

	// Should handle gracefully
	assert.NoError(t, redisMock.ExpectationsWereMet())
	mockQueue.AssertExpectations(t)
}

// Integration test that combines multiple operations
func TestPaymentService_CreateAndProcessPayment_Integration(t *testing.T) {
	service, redisMock, mockQueue, mockPubNub := setupTestPaymentService()
	defer redisMock.ClearExpected()

	ctx := context.Background()
	userID := "integration-user"
	eventID := "integration-event"
	seats := []string{"A1", "A2"}
	amount := 150.75

	// Step 1: Create payment session
	redisMock.ExpectHSet(mock.AnythingOfType("string"), map[string]interface{}{
		"payment_id": mock.AnythingOfType("string"),
		"user_id":    userID,
		"event_id":   eventID,
		"seats":      seats,
		"amount":     amount,
		"status":     "pending",
		"created_at": mock.AnythingOfType("int64"),
	}).SetVal(7)

	redisMock.ExpectExpire(mock.AnythingOfType("string"), 5*time.Minute).SetVal(true)

	paymentID, err := service.CreatePaymentSession(ctx, userID, eventID, seats, amount)
	require.NoError(t, err)

	// Step 2: Process successful payment notification
	messageData := map[string]interface{}{
		"payment_id": paymentID,
		"status":     "success",
	}

	message := &pubnub.PNMessage{
		Message: messageData,
	}

	// Mock payment data retrieval
	seatsJSON, _ := json.Marshal(seats)
	paymentData := map[string]string{
		"payment_id": paymentID,
		"user_id":    userID,
		"event_id":   eventID,
		"seats":      string(seatsJSON),
		"amount":     fmt.Sprintf("%.2f", amount),
		"status":     "pending",
	}

	redisMock.ExpectHGetAll("payment:" + paymentID).SetVal(paymentData)

	// Mock seat operations
	for _, seatID := range seats {
		seatKey := fmt.Sprintf("seat:%s:%s", eventID, seatID)
		redisMock.ExpectHSet(seatKey, map[string]interface{}{
			"status":     "sold",
			"user_id":    userID,
			"locked_at":  mock.Anything,
			"sold_at":    mock.Anything,
		}).SetVal(4)
	}

	// Mock payment completion
	redisMock.ExpectHSet("payment:"+paymentID, "status", "completed").SetVal(1)

	// Mock queue operations
	mockQueue.On("RemoveFromProcessing", mock.Anything, eventID, userID).Return(nil)
	mockQueue.On("ProcessQueue", mock.Anything, eventID).Return()

	// Execute payment processing
	service.handlePaymentNotification(message)

	// Wait for async operations
	time.Sleep(100 * time.Millisecond)

	// Verify all operations completed successfully
	assert.NoError(t, redisMock.ExpectationsWereMet())
	mockQueue.AssertExpectations(t)
	assert.Len(t, mockPubNub.publishCalls, 1)
}

// Benchmark tests
func BenchmarkPaymentService_CreatePaymentSession(b *testing.B) {
	service, redisMock, _, _ := setupTestPaymentService()
	defer redisMock.ClearExpected()

	ctx := context.Background()
	userID := "bench-user"
	eventID := "bench-event"
	seats := []string{"A1", "A2"}
	amount := 100.0

	// Setup mock expectations for benchmark
	for i := 0; i < b.N; i++ {
		redisMock.ExpectHSet(mock.AnythingOfType("string"), mock.Anything).SetVal(7)
		redisMock.ExpectExpire(mock.AnythingOfType("string"), 5*time.Minute).SetVal(true)
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		service.CreatePaymentSession(ctx, fmt.Sprintf("%s-%d", userID, i), eventID, seats, amount)
	}
}

func BenchmarkPaymentService_HandlePaymentNotification(b *testing.B) {
	service, redisMock, mockQueue, _ := setupTestPaymentService()
	defer redisMock.ClearExpected()

	// Setup mock expectations for benchmark
	for i := 0; i < b.N; i++ {
		paymentData := map[string]string{
			"payment_id": fmt.Sprintf("payment_%d", i),
			"user_id":    "bench-user",
			"event_id":   "bench-event",
			"seats":      `["A1","A2"]`,
			"amount":     "100.0",
			"status":     "pending",
		}

		redisMock.ExpectHGetAll(mock.AnythingOfType("string")).SetVal(paymentData)
		
		// Mock seat operations
		for j := 1; j <= 2; j++ {
			redisMock.ExpectHSet(mock.AnythingOfType("string"), mock.Anything).SetVal(4)
		}
		
		redisMock.ExpectHSet(mock.AnythingOfType("string"), "status", "completed").SetVal(1)
		mockQueue.On("RemoveFromProcessing", mock.Anything, "bench-event", "bench-user").Return(nil)
		mockQueue.On("ProcessQueue", mock.Anything, "bench-event").Return()
	}

	messages := make([]*pubnub.PNMessage, b.N)
	for i := 0; i < b.N; i++ {
		messages[i] = &pubnub.PNMessage{
			Message: map[string]interface{}{
				"payment_id": fmt.Sprintf("payment_%d", i),
				"status":     "success",
			},
		}
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		service.handlePaymentNotification(messages[i])
	}
}