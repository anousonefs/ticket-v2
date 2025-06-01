package models

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestEvent_JSONSerialization(t *testing.T) {
	startTime := time.Now()
	endTime := startTime.Add(2 * time.Hour)

	event := Event{
		ID:          "event-123",
		Name:        "Test Concert",
		Description: "A great test concert",
		Venue:       "Test Arena",
		StartTime:   startTime,
		EndTime:     endTime,
		TotalSeats:  1000,
		Status:      "upcoming",
	}

	// Test JSON marshaling
	jsonData, err := json.Marshal(event)
	require.NoError(t, err)

	// Test JSON unmarshaling
	var unmarshaled Event
	err = json.Unmarshal(jsonData, &unmarshaled)
	require.NoError(t, err)

	// Verify all fields
	assert.Equal(t, event.ID, unmarshaled.ID)
	assert.Equal(t, event.Name, unmarshaled.Name)
	assert.Equal(t, event.Description, unmarshaled.Description)
	assert.Equal(t, event.Venue, unmarshaled.Venue)
	assert.Equal(t, event.TotalSeats, unmarshaled.TotalSeats)
	assert.Equal(t, event.Status, unmarshaled.Status)
	
	// Time comparison with some tolerance for JSON serialization
	assert.WithinDuration(t, event.StartTime, unmarshaled.StartTime, time.Second)
	assert.WithinDuration(t, event.EndTime, unmarshaled.EndTime, time.Second)
}

func TestEvent_ValidStatuses(t *testing.T) {
	validStatuses := []string{"upcoming", "ongoing", "completed"}
	
	for _, status := range validStatuses {
		event := Event{
			ID:     "test-event",
			Status: status,
		}
		
		// Test that valid statuses can be set
		assert.Equal(t, status, event.Status)
		
		// Test JSON serialization preserves status
		jsonData, err := json.Marshal(event)
		require.NoError(t, err)
		
		var unmarshaled Event
		err = json.Unmarshal(jsonData, &unmarshaled)
		require.NoError(t, err)
		assert.Equal(t, status, unmarshaled.Status)
	}
}

func TestPayment_JSONSerialization(t *testing.T) {
	createdAt := time.Now()
	expiresAt := createdAt.Add(5 * time.Minute)
	completedAt := createdAt.Add(2 * time.Minute)

	payment := Payment{
		ID:            "payment-123",
		UserID:        "user-456",
		EventID:       "event-789",
		Seats:         []string{"A1", "A2", "B3"},
		Amount:        150.50,
		Status:        "completed",
		PaymentMethod: "qr_code",
		QRCode:        "qr-code-data",
		CreatedAt:     createdAt,
		ExpiresAt:     expiresAt,
		CompletedAt:   &completedAt,
	}

	// Test JSON marshaling
	jsonData, err := json.Marshal(payment)
	require.NoError(t, err)

	// Test JSON unmarshaling
	var unmarshaled Payment
	err = json.Unmarshal(jsonData, &unmarshaled)
	require.NoError(t, err)

	// Verify all fields
	assert.Equal(t, payment.ID, unmarshaled.ID)
	assert.Equal(t, payment.UserID, unmarshaled.UserID)
	assert.Equal(t, payment.EventID, unmarshaled.EventID)
	assert.Equal(t, payment.Seats, unmarshaled.Seats)
	assert.Equal(t, payment.Amount, unmarshaled.Amount)
	assert.Equal(t, payment.Status, unmarshaled.Status)
	assert.Equal(t, payment.PaymentMethod, unmarshaled.PaymentMethod)
	assert.Equal(t, payment.QRCode, unmarshaled.QRCode)
	
	// Time comparison with tolerance
	assert.WithinDuration(t, payment.CreatedAt, unmarshaled.CreatedAt, time.Second)
	assert.WithinDuration(t, payment.ExpiresAt, unmarshaled.ExpiresAt, time.Second)
	require.NotNil(t, unmarshaled.CompletedAt)
	assert.WithinDuration(t, *payment.CompletedAt, *unmarshaled.CompletedAt, time.Second)
}

func TestPayment_NilCompletedAt(t *testing.T) {
	payment := Payment{
		ID:            "payment-123",
		UserID:        "user-456",
		EventID:       "event-789",
		Seats:         []string{"A1"},
		Amount:        50.0,
		Status:        "pending",
		PaymentMethod: "credit_card",
		CreatedAt:     time.Now(),
		ExpiresAt:     time.Now().Add(5 * time.Minute),
		CompletedAt:   nil, // Not completed yet
	}

	// Test JSON marshaling with nil CompletedAt
	jsonData, err := json.Marshal(payment)
	require.NoError(t, err)

	// Test JSON unmarshaling
	var unmarshaled Payment
	err = json.Unmarshal(jsonData, &unmarshaled)
	require.NoError(t, err)

	// Verify CompletedAt is nil
	assert.Nil(t, unmarshaled.CompletedAt)
	assert.Equal(t, "pending", unmarshaled.Status)
}

func TestPayment_ValidStatuses(t *testing.T) {
	validStatuses := []string{"pending", "processing", "completed", "failed"}
	
	for _, status := range validStatuses {
		payment := Payment{
			ID:     "test-payment",
			Status: status,
		}
		
		assert.Equal(t, status, payment.Status)
	}
}

func TestPayment_ValidPaymentMethods(t *testing.T) {
	validMethods := []string{"qr_code", "credit_card", "bank_transfer"}
	
	for _, method := range validMethods {
		payment := Payment{
			ID:            "test-payment",
			PaymentMethod: method,
		}
		
		assert.Equal(t, method, payment.PaymentMethod)
	}
}

func TestPaymentNotification_JSONSerialization(t *testing.T) {
	timestamp := time.Now()
	
	notification := PaymentNotification{
		PaymentID:     "payment-123",
		Status:        "completed",
		TransactionID: "txn-456",
		Timestamp:     timestamp,
	}

	// Test JSON marshaling
	jsonData, err := json.Marshal(notification)
	require.NoError(t, err)

	// Test JSON unmarshaling
	var unmarshaled PaymentNotification
	err = json.Unmarshal(jsonData, &unmarshaled)
	require.NoError(t, err)

	// Verify all fields
	assert.Equal(t, notification.PaymentID, unmarshaled.PaymentID)
	assert.Equal(t, notification.Status, unmarshaled.Status)
	assert.Equal(t, notification.TransactionID, unmarshaled.TransactionID)
	assert.WithinDuration(t, notification.Timestamp, unmarshaled.Timestamp, time.Second)
}

func TestTicket_JSONSerialization(t *testing.T) {
	createdAt := time.Now()
	paidAt := createdAt.Add(1 * time.Hour)

	ticket := Ticket{
		ID:        "ticket-123",
		EventID:   "event-456",
		UserID:    "user-789",
		SeatID:    "A1",
		Price:     75.25,
		Status:    "paid",
		CreatedAt: createdAt,
		PaidAt:    &paidAt,
	}

	// Test JSON marshaling
	jsonData, err := json.Marshal(ticket)
	require.NoError(t, err)

	// Test JSON unmarshaling
	var unmarshaled Ticket
	err = json.Unmarshal(jsonData, &unmarshaled)
	require.NoError(t, err)

	// Verify all fields
	assert.Equal(t, ticket.ID, unmarshaled.ID)
	assert.Equal(t, ticket.EventID, unmarshaled.EventID)
	assert.Equal(t, ticket.UserID, unmarshaled.UserID)
	assert.Equal(t, ticket.SeatID, unmarshaled.SeatID)
	assert.Equal(t, ticket.Price, unmarshaled.Price)
	assert.Equal(t, ticket.Status, unmarshaled.Status)
	assert.WithinDuration(t, ticket.CreatedAt, unmarshaled.CreatedAt, time.Second)
	require.NotNil(t, unmarshaled.PaidAt)
	assert.WithinDuration(t, *ticket.PaidAt, *unmarshaled.PaidAt, time.Second)
}

func TestTicket_NilPaidAt(t *testing.T) {
	ticket := Ticket{
		ID:        "ticket-123",
		EventID:   "event-456",
		UserID:    "user-789",
		SeatID:    "A1",
		Price:     75.25,
		Status:    "reserved",
		CreatedAt: time.Now(),
		PaidAt:    nil, // Not paid yet
	}

	// Test JSON marshaling with nil PaidAt
	jsonData, err := json.Marshal(ticket)
	require.NoError(t, err)

	// Test JSON unmarshaling
	var unmarshaled Ticket
	err = json.Unmarshal(jsonData, &unmarshaled)
	require.NoError(t, err)

	// Verify PaidAt is nil
	assert.Nil(t, unmarshaled.PaidAt)
	assert.Equal(t, "reserved", unmarshaled.Status)
}

func TestTicket_ValidStatuses(t *testing.T) {
	validStatuses := []string{"reserved", "paid", "cancelled"}
	
	for _, status := range validStatuses {
		ticket := Ticket{
			ID:     "test-ticket",
			Status: status,
		}
		
		assert.Equal(t, status, ticket.Status)
	}
}

func TestSeat_JSONSerialization(t *testing.T) {
	lockedAt := time.Now()

	seat := Seat{
		ID:       "A1",
		EventID:  "event-123",
		Row:      "A",
		Number:   1,
		Section:  "VIP",
		Price:    100.0,
		Status:   "locked",
		LockedBy: "user-456",
		LockedAt: &lockedAt,
	}

	// Test JSON marshaling
	jsonData, err := json.Marshal(seat)
	require.NoError(t, err)

	// Test JSON unmarshaling
	var unmarshaled Seat
	err = json.Unmarshal(jsonData, &unmarshaled)
	require.NoError(t, err)

	// Verify all fields
	assert.Equal(t, seat.ID, unmarshaled.ID)
	assert.Equal(t, seat.EventID, unmarshaled.EventID)
	assert.Equal(t, seat.Row, unmarshaled.Row)
	assert.Equal(t, seat.Number, unmarshaled.Number)
	assert.Equal(t, seat.Section, unmarshaled.Section)
	assert.Equal(t, seat.Price, unmarshaled.Price)
	assert.Equal(t, seat.Status, unmarshaled.Status)
	assert.Equal(t, seat.LockedBy, unmarshaled.LockedBy)
	require.NotNil(t, unmarshaled.LockedAt)
	assert.WithinDuration(t, *seat.LockedAt, *unmarshaled.LockedAt, time.Second)
}

func TestSeat_AvailableStatus(t *testing.T) {
	seat := Seat{
		ID:       "B2",
		EventID:  "event-123",
		Row:      "B",
		Number:   2,
		Section:  "General",
		Price:    50.0,
		Status:   "available",
		LockedBy: "", // Empty for available seats
		LockedAt: nil, // Nil for available seats
	}

	// Test JSON marshaling
	jsonData, err := json.Marshal(seat)
	require.NoError(t, err)

	// Test JSON unmarshaling
	var unmarshaled Seat
	err = json.Unmarshal(jsonData, &unmarshaled)
	require.NoError(t, err)

	// Verify available seat properties
	assert.Equal(t, "available", unmarshaled.Status)
	assert.Empty(t, unmarshaled.LockedBy)
	assert.Nil(t, unmarshaled.LockedAt)
}

func TestSeat_ValidStatuses(t *testing.T) {
	validStatuses := []string{"available", "locked", "sold"}
	
	for _, status := range validStatuses {
		seat := Seat{
			ID:     "test-seat",
			Status: status,
		}
		
		assert.Equal(t, status, seat.Status)
	}
}

func TestQueueEntry_JSONSerialization(t *testing.T) {
	joinedAt := time.Now()

	entry := QueueEntry{
		UserID:    "user-123",
		EventID:   "event-456",
		JoinedAt:  joinedAt,
		Position:  5,
		Status:    "waiting",
		SessionID: "session-789",
	}

	// Test JSON marshaling
	jsonData, err := json.Marshal(entry)
	require.NoError(t, err)

	// Test JSON unmarshaling
	var unmarshaled QueueEntry
	err = json.Unmarshal(jsonData, &unmarshaled)
	require.NoError(t, err)

	// Verify all fields
	assert.Equal(t, entry.UserID, unmarshaled.UserID)
	assert.Equal(t, entry.EventID, unmarshaled.EventID)
	assert.WithinDuration(t, entry.JoinedAt, unmarshaled.JoinedAt, time.Second)
	assert.Equal(t, entry.Position, unmarshaled.Position)
	assert.Equal(t, entry.Status, unmarshaled.Status)
	assert.Equal(t, entry.SessionID, unmarshaled.SessionID)
}

func TestQueueEntry_ValidStatuses(t *testing.T) {
	validStatuses := []string{"waiting", "processing", "completed"}
	
	for _, status := range validStatuses {
		entry := QueueEntry{
			UserID: "test-user",
			Status: status,
		}
		
		assert.Equal(t, status, entry.Status)
	}
}

func TestProcessingUser_JSONSerialization(t *testing.T) {
	startedAt := time.Now()

	user := ProcessingUser{
		UserID:      "user-123",
		EventID:     "event-456",
		StartedAt:   startedAt,
		LockedSeats: []string{"A1", "A2", "B3"},
		SessionID:   "session-789",
	}

	// Test JSON marshaling
	jsonData, err := json.Marshal(user)
	require.NoError(t, err)

	// Test JSON unmarshaling
	var unmarshaled ProcessingUser
	err = json.Unmarshal(jsonData, &unmarshaled)
	require.NoError(t, err)

	// Verify all fields
	assert.Equal(t, user.UserID, unmarshaled.UserID)
	assert.Equal(t, user.EventID, unmarshaled.EventID)
	assert.WithinDuration(t, user.StartedAt, unmarshaled.StartedAt, time.Second)
	assert.Equal(t, user.LockedSeats, unmarshaled.LockedSeats)
	assert.Equal(t, user.SessionID, unmarshaled.SessionID)
}

func TestProcessingUser_EmptyLockedSeats(t *testing.T) {
	user := ProcessingUser{
		UserID:      "user-123",
		EventID:     "event-456",
		StartedAt:   time.Now(),
		LockedSeats: []string{}, // Empty slice
		SessionID:   "session-789",
	}

	// Test JSON marshaling
	jsonData, err := json.Marshal(user)
	require.NoError(t, err)

	// Test JSON unmarshaling
	var unmarshaled ProcessingUser
	err = json.Unmarshal(jsonData, &unmarshaled)
	require.NoError(t, err)

	// Verify empty slice is preserved
	assert.NotNil(t, unmarshaled.LockedSeats)
	assert.Len(t, unmarshaled.LockedSeats, 0)
}

func TestQueueMetrics_JSONSerialization(t *testing.T) {
	lastUpdated := time.Now()

	metrics := QueueMetrics{
		EventID:         "event-123",
		TotalInQueue:    25,
		ProcessingCount: 5,
		AvgWaitTime:     120.5,
		LastUpdated:     lastUpdated,
	}

	// Test JSON marshaling
	jsonData, err := json.Marshal(metrics)
	require.NoError(t, err)

	// Test JSON unmarshaling
	var unmarshaled QueueMetrics
	err = json.Unmarshal(jsonData, &unmarshaled)
	require.NoError(t, err)

	// Verify all fields
	assert.Equal(t, metrics.EventID, unmarshaled.EventID)
	assert.Equal(t, metrics.TotalInQueue, unmarshaled.TotalInQueue)
	assert.Equal(t, metrics.ProcessingCount, unmarshaled.ProcessingCount)
	assert.Equal(t, metrics.AvgWaitTime, unmarshaled.AvgWaitTime)
	assert.WithinDuration(t, metrics.LastUpdated, unmarshaled.LastUpdated, time.Second)
}

// Test edge cases and validation
func TestModels_ZeroValues(t *testing.T) {
	// Test that zero values don't break JSON serialization
	tests := []interface{}{
		Event{},
		Payment{},
		PaymentNotification{},
		Ticket{},
		Seat{},
		QueueEntry{},
		ProcessingUser{},
		QueueMetrics{},
	}

	for _, test := range tests {
		jsonData, err := json.Marshal(test)
		assert.NoError(t, err)
		assert.NotEmpty(t, jsonData)
		
		// Should be able to unmarshal back
		err = json.Unmarshal(jsonData, &test)
		assert.NoError(t, err)
	}
}

// Test that time pointers handle nil values correctly
func TestModels_NilTimePointers(t *testing.T) {
	// Test Payment with nil CompletedAt
	payment := Payment{CompletedAt: nil}
	jsonData, err := json.Marshal(payment)
	assert.NoError(t, err)
	
	var unmarshaledPayment Payment
	err = json.Unmarshal(jsonData, &unmarshaledPayment)
	assert.NoError(t, err)
	assert.Nil(t, unmarshaledPayment.CompletedAt)

	// Test Ticket with nil PaidAt
	ticket := Ticket{PaidAt: nil}
	jsonData, err = json.Marshal(ticket)
	assert.NoError(t, err)
	
	var unmarshaledTicket Ticket
	err = json.Unmarshal(jsonData, &unmarshaledTicket)
	assert.NoError(t, err)
	assert.Nil(t, unmarshaledTicket.PaidAt)

	// Test Seat with nil LockedAt
	seat := Seat{LockedAt: nil}
	jsonData, err = json.Marshal(seat)
	assert.NoError(t, err)
	
	var unmarshaledSeat Seat
	err = json.Unmarshal(jsonData, &unmarshaledSeat)
	assert.NoError(t, err)
	assert.Nil(t, unmarshaledSeat.LockedAt)
}

// Benchmark tests for JSON operations
func BenchmarkEvent_JSONMarshal(b *testing.B) {
	event := Event{
		ID:          "event-123",
		Name:        "Benchmark Concert",
		Description: "A benchmarking event",
		Venue:       "Benchmark Arena",
		StartTime:   time.Now(),
		EndTime:     time.Now().Add(2 * time.Hour),
		TotalSeats:  1000,
		Status:      "upcoming",
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		json.Marshal(event)
	}
}

func BenchmarkPayment_JSONMarshal(b *testing.B) {
	payment := Payment{
		ID:            "payment-123",
		UserID:        "user-456",
		EventID:       "event-789",
		Seats:         []string{"A1", "A2", "B3"},
		Amount:        150.50,
		Status:        "completed",
		PaymentMethod: "qr_code",
		CreatedAt:     time.Now(),
		ExpiresAt:     time.Now().Add(5 * time.Minute),
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		json.Marshal(payment)
	}
}

func BenchmarkQueueEntry_JSONMarshal(b *testing.B) {
	entry := QueueEntry{
		UserID:    "user-123",
		EventID:   "event-456",
		JoinedAt:  time.Now(),
		Position:  5,
		Status:    "waiting",
		SessionID: "session-789",
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		json.Marshal(entry)
	}
}