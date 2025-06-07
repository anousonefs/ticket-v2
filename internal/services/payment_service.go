package services

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log/slog"
	"ticket-system/internal/services/bank"
	"ticket-system/internal/status"
	"ticket-system/utils"
	"time"

	"github.com/redis/go-redis/v9"

	// Adjust import path

	pubnub "github.com/pubnub/go"
	"github.com/shopspring/decimal"
)

type Payment interface {
	GenQRCode(ctx context.Context, param *status.FormQR) (string, error)
	SetTranChannel(ch chan *status.Transaction)
}

type PaymentService struct {
	Redis      *redis.Client
	PubNub     *pubnub.PubNub
	queue      *QueueService
	banks      *bank.BankRegistry
	seat       *SeatService
	logger     *slog.Logger
	maxRetries int
	retryDelay time.Duration
}

func NewPaymentService(redisClient *redis.Client, pn *pubnub.PubNub, queueService *QueueService, banks *bank.BankRegistry, seat *SeatService) *PaymentService {
	if banks == nil {
		panic("banks instance must not be nil")
	}
	service := &PaymentService{
		Redis:      redisClient,
		PubNub:     pn,
		queue:      queueService,
		banks:      banks,
		seat:       seat,
		logger:     slog.Default(),
		maxRetries: 3,
		retryDelay: 2 * time.Second,
	}

	go service.SubscribeToPaymentNotifications2()

	return service
}

type PaymentNotification struct {
	RefID     string `json:"ref_id"`
	Status    string `json:"status"`
	Amount    string `json:"amount"`
	Timestamp int64  `json:"timestamp"`
}

type PaymentNotificationHandler struct {
	redis      *redis.Client
	pubnub     *pubnub.PubNub
	queue      QueueServiceInterface
	seat       *SeatService
	db         DatabaseInterface // For SQL operations
	logger     *slog.Logger
	maxRetries int
	retryDelay time.Duration
}

// NewPaymentNotificationHandler creates a new payment notification handler
func NewPaymentNotificationHandler(
	redis *redis.Client,
	pubnub *pubnub.PubNub,
	queue QueueServiceInterface,
	seat *SeatService,
	db DatabaseInterface,
	logger *slog.Logger,
) *PaymentNotificationHandler {
	return &PaymentNotificationHandler{
		redis:      redis,
		pubnub:     pubnub,
		queue:      queue,
		seat:       seat,
		db:         db,
		logger:     logger,
		maxRetries: 3,
		retryDelay: time.Second * 2,
	}
}

func (s *PaymentService) CreatePaymentSession(ctx context.Context, userID, eventID string, seats []string, amount float64) (string, error) {
	paymentID, _ := utils.GenerateCode(8)

	paymentData := map[string]any{
		"payment_id": paymentID,
		"user_id":    userID,
		"event_id":   eventID,
		"seats":      seats,
		"amount":     amount,
		"status":     "pending",
		"created_at": time.Now().Unix(),
	}

	paymentKey := fmt.Sprintf("payment:%s", paymentID)
	for k, v := range paymentData {
		s.Redis.HSet(ctx, paymentKey, k, v)
	}

	// todo: use constance timeout, if timeout then unlock seat
	s.Redis.Expire(ctx, paymentKey, 5*time.Minute)

	return paymentID, nil
}

type QRRequest struct {
	PaymentID string            `json:"payment_id"`
	BookID    string            `json:"book_id"`
	Phone     string            `json:"phone"`
	Amount    decimal.Decimal   `json:"amount"`
	BankName  bank.BankProvider `json:"bank_name"`
}

func (s *PaymentService) GenQR(ctx context.Context, params QRRequest) (string, error) {
	refID, _ := utils.GenerateCode(4)

	paymentReq := &bank.PaymentRequest{
		Phone:           params.Phone,
		ReferenceNumber: refID,
		TerminalLabel:   refID,
		UUID:            params.BookID,
		Amount:          params.Amount,
		Currency:        "LAK",
		Description:     "enter event",
		IsDeepLink:      true,
		MerchantID:      "",
	}

	var bankName bank.BankProvider
	switch params.BankName {
	case bank.BankJDB:
		bankName = bank.BankJDB
		slog.Info("bank name", "name", bank.BankJDB)
	case bank.BankLDB:
		slog.Info("bank name", "name", bank.BankLDB)
		bankName = bank.BankLDB
	case bank.BankBCEL:
		bankName = bank.BankBCEL
		slog.Info("bank name", "name", bank.BankBCEL)
	default:
		bankName = bank.BankJDB
		slog.Info("default bank name", "name", bank.BankJDB)
	}

	bank, err := s.banks.GetBank(bankName)
	if err != nil {
		return "", err
	}

	envCode, err := bank.GenerateQR(ctx, paymentReq)
	if err != nil {
		return "", err
	}

	return envCode, nil
}

// PaymentData represents payment information stored in Redis
type PaymentData struct {
	UserID    string   `json:"user_id"`
	EventID   string   `json:"event_id"`
	Seats     []string `json:"seats"`
	Amount    string   `json:"amount"`
	Status    string   `json:"status"`
	CreatedAt string   `json:"created_at"`
}

// SubscribeToPaymentNotifications starts listening for payment notifications
func (s *PaymentService) SubscribeToPaymentNotifications2() {
	if s.banks == nil {
		s.logger.Error("payment service is nil, cannot subscribe to notifications")
		return
	}

	go func() {
		defer func() {
			if r := recover(); r != nil {
				s.logger.Error("payment notification subscription panic recovered", "panic", r)
				// Restart subscription after delay
				time.Sleep(5 * time.Second)
				s.SubscribeToPaymentNotifications2()
			}
		}()

		txChannel := make(chan *status.Transaction, 10) // Buffered channel
		// s.banks.SetTranChannel(txChannel)

		s.logger.Info("payment notification subscription started")

		for {
			select {
			case t, ok := <-txChannel:
				if !ok {
					s.logger.Warn("payment notification channel closed")
					return
				}

				s.logger.Info("payment notification received",
					"transaction_id", t.UUID,
					"ref_id", t.RefID,
				)

				// Handle notification in separate goroutine with error recovery
				go s.handlePaymentNotificationSafely(t)

			case <-time.After(30 * time.Minute):
				// Heartbeat to ensure the subscription is alive
				s.logger.Debug("payment notification subscription heartbeat")
			}
		}
	}()
}

// handlePaymentNotificationSafely wraps the notification handler with error recovery
func (s *PaymentService) handlePaymentNotificationSafely(transaction *status.Transaction) {
	defer func() {
		if r := recover(); r != nil {
			s.logger.Error("payment notification handler panic recovered",
				"panic", r,
				"transaction_id", transaction.UUID,
				"ref_id", transaction.RefID,
			)
		}
	}()

	// Create context with timeout
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	if err := s.handlePaymentNotification2(ctx, transaction); err != nil {
		s.logger.Error("failed to handle payment notification",
			"error", err,
			"transaction_id", transaction.UUID,
			"ref_id", transaction.RefID,
		)
	}
}

// handlePaymentNotification processes the payment notification
func (s *PaymentService) handlePaymentNotification2(ctx context.Context, transaction *status.Transaction) error {
	// Validate input
	if transaction == nil || transaction.UUID == "" {
		return fmt.Errorf("invalid transaction data")
	}

	// Get payment data from Redis
	paymentData, err := s.getPaymentData(ctx, transaction.UUID)
	if err != nil {
		return fmt.Errorf("failed to get payment data: %w", err)
	}

	s.logger.Info("processing payment notification",
		"payment_id", transaction.UUID,
		"user_id", paymentData.UserID,
		"event_id", paymentData.EventID,
		"seats_count", len(paymentData.Seats),
	)

	// Process payment success
	if err := s.processPaymentSuccess(ctx, transaction.UUID, paymentData); err != nil {
		return fmt.Errorf("failed to process payment success: %w", err)
	}

	s.logger.Info("payment notification processed successfully",
		"payment_id", transaction.UUID,
		"user_id", paymentData.UserID,
	)

	return nil
}

// getPaymentData retrieves and validates payment data from Redis
func (s *PaymentService) getPaymentData(ctx context.Context, refID string) (*PaymentData, error) {
	paymentKey := fmt.Sprintf("payment:%s", refID)

	// Get all payment data
	rawData := s.Redis.HGetAll(ctx, paymentKey).Val()
	if len(rawData) == 0 {
		return nil, fmt.Errorf("payment data not found for ref_id: %s", refID)
	}

	// Parse seats JSON
	var seats []string
	if seatsJSON, exists := rawData["seats"]; exists && seatsJSON != "" {
		if err := json.Unmarshal([]byte(seatsJSON), &seats); err != nil {
			return nil, fmt.Errorf("failed to parse seats JSON: %w", err)
		}
	}

	// Validate required fields
	userID := rawData["user_id"]
	eventID := rawData["event_id"]

	if userID == "" || eventID == "" {
		return nil, fmt.Errorf("missing required payment data fields")
	}

	return &PaymentData{
		UserID:    userID,
		EventID:   eventID,
		Seats:     seats,
		Amount:    rawData["amount"],
		Status:    rawData["status"],
		CreatedAt: rawData["created_at"],
	}, nil
}

// processPaymentSuccess handles successful payment processing
func (s *PaymentService) processPaymentSuccess(ctx context.Context, paymentId string, paymentData *PaymentData) error {
	// tx := s.db.BeginTx(ctx)
	// defer func() {
	// 	if r := recover(); r != nil {
	// 		tx.Rollback()
	// 		panic(r)
	// 	}
	// }()
	//
	// if err := s.markSeatsAsSold(ctx, paymentData); err != nil {
	// 	tx.Rollback()
	// 	return fmt.Errorf("failed to mark seats as sold: %w", err)
	// }
	//
	// if err := s.updatePaymentStatus(ctx, refID, "completed"); err != nil {
	// 	tx.Rollback()
	// 	return fmt.Errorf("failed to update payment status in Redis: %w", err)
	// }
	//
	// if err := s.updatePaymentStatusInDB(ctx, tx, refID, "completed"); err != nil {
	// 	tx.Rollback()
	// 	return fmt.Errorf("failed to update payment status in database: %w", err)
	// }
	//
	// if err := s.updateSeatsStatusInDB(ctx, tx, paymentData); err != nil {
	// 	tx.Rollback()
	// 	return fmt.Errorf("failed to update seats status in database: %w", err)
	// }

	if err := s.queue.RemoveFromProcessing(ctx, paymentData.EventID, paymentData.UserID); err != nil {
		s.logger.Error("failed to remove user from processing queue",
			"error", err,
			"event_id", paymentData.EventID,
			"user_id", paymentData.UserID,
		)
	}

	// if err := tx.Commit(); err != nil {
	// 	return fmt.Errorf("failed to commit transaction: %w", err)
	// }

	s.queue.TriggerProcessQueue(paymentData.EventID)

	if err := s.sendPaymentSuccessNotification(paymentData.UserID, paymentId, paymentData.Seats); err != nil {
		s.logger.Error("failed to send payment success notification",
			"error", err,
			"user_id", paymentData.UserID,
			"ref_id", paymentId,
		)
	}

	return nil
}

// markSeatsAsSold marks all seats as sold in Redis
func (s *PaymentService) markSeatsAsSold(ctx context.Context, paymentData *PaymentData) error {
	for _, seatID := range paymentData.Seats {
		if err := s.seat.MarkSeatAsSold(ctx, paymentData.EventID, seatID, paymentData.UserID); err != nil {
			return fmt.Errorf("failed to mark seat %s as sold: %w", seatID, err)
		}
	}
	return nil
}

// updatePaymentStatus updates payment status in Redis
func (s *PaymentService) updatePaymentStatus(ctx context.Context, refID, status string) error {
	paymentKey := fmt.Sprintf("payment:%s", refID)
	return s.Redis.HSet(ctx, paymentKey, map[string]interface{}{
		"status":     status,
		"updated_at": time.Now().Unix(),
	}).Err()
}

// updatePaymentStatusInDB updates payment status in SQL database
func (s *PaymentService) updatePaymentStatusInDB(ctx context.Context, tx DatabaseTx, refID, status string) error {
	query := `UPDATE payments SET status = $1, updated_at = NOW() WHERE ref_id = $2`
	_, err := tx.ExecContext(ctx, query, status, refID)
	return err
}

// updateSeatsStatusInDB updates seat statuses in SQL database
func (s *PaymentService) updateSeatsStatusInDB(ctx context.Context, tx DatabaseTx, paymentData *PaymentData) error {
	if len(paymentData.Seats) == 0 {
		return nil
	}

	// Build bulk update query
	query := `UPDATE seats SET status = 'sold', sold_to = $1, sold_at = NOW() 
			  WHERE event_id = $2 AND seat_id = ANY($3)`

	_, err := tx.ExecContext(ctx, query, paymentData.UserID, paymentData.EventID, paymentData.Seats)
	return err
}

// sendPaymentSuccessNotification sends success notification via PubNub
func (s *PaymentService) sendPaymentSuccessNotification(userID, paymentId string, seats []string) error {
	channel := fmt.Sprintf("user-%s", userID)

	message := map[string]interface{}{
		"type":       "payment_success",
		"payment_id": paymentId,
		"seats":      seats,
		"timestamp":  time.Now().Unix(),
	}

	// Retry mechanism for PubNub publishing
	for attempt := 0; attempt < s.maxRetries; attempt++ {
		result, _, err := s.PubNub.Publish().
			Channel(channel).
			Message(message).
			Execute()

		if err == nil && result != nil {
			s.logger.Info("payment success notification sent",
				"user_id", userID,
				"channel", channel,
				"ref_id", paymentId,
			)
			return nil
		}

		s.logger.Warn("failed to send payment notification, retrying",
			"attempt", attempt+1,
			"error", err,
			"user_id", userID,
		)

		if attempt < s.maxRetries-1 {
			time.Sleep(s.retryDelay)
		}
	}

	return fmt.Errorf("failed to send payment notification after %d attempts", s.maxRetries)
}

// Interfaces for dependency injection and testing
type QueueServiceInterface interface {
	RemoveFromProcessing(ctx context.Context, eventID, userID string) error
	TriggerProcessQueue(eventID string)
}

type DatabaseInterface interface {
	BeginTx(ctx context.Context) DatabaseTx
}

type DatabaseTx interface {
	ExecContext(ctx context.Context, query string, args ...interface{}) (sql.Result, error)
	Commit() error
	Rollback() error
}
