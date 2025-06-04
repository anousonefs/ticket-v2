package services

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"ticket-system/internal/services/bank/jdb"
	"ticket-system/utils"
	"time"

	pubnub "github.com/pubnub/go"
	"github.com/redis/go-redis/v9"
	"github.com/shopspring/decimal"
)

type PaymentService struct {
	Redis   *redis.Client
	PubNub  *pubnub.PubNub
	queue   *QueueService
	payment *jdb.Yespay
}

func NewPaymentService(redisClient *redis.Client, pn *pubnub.PubNub, queueService *QueueService, jdbInstance *jdb.Yespay) *PaymentService {
	if jdbInstance == nil {
		panic("jdb instance must not be nil")
	}
	service := &PaymentService{
		Redis:   redisClient,
		PubNub:  pn,
		queue:   queueService,
		payment: jdbInstance,
	}

	go service.SubscribeToPaymentNotifications()

	return service
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

func (s *PaymentService) SubscribeToPaymentNotifications() {
	if s.payment != nil {
		go func() {
			txChannel := make(chan *jdb.Transaction, 1)
			s.payment.SetTranChannel(txChannel)
			for {
				select {
				case t := <-txChannel:
					slog.Info("=> yespay retrieve transaction", "txChannel", t)

					// ctx := context.Background()
					// if err := s.YesPay(ctx, t.UUID); err != nil {
					// 	slog.Error("yespay sub paymentService.YesPay()", "error", err)
					// }

					go s.handlePaymentNotification(t)
				}
			}
		}()
	}
}

type GenJdbQrRequest struct {
	PaymentID string          `json:"payment_id"`
	BookID    string          `json:"book_id"`
	Phone     string          `json:"phone"`
	Amount    decimal.Decimal `json:"amount"`
}

func (s *PaymentService) GenJdbQr(ctx context.Context, params GenJdbQrRequest) (string, error) {
	refID, _ := utils.GenerateCode(4)

	f := &jdb.FormQR{
		Phone:          params.Phone,
		ReferenceLabel: fmt.Sprintf("%s-%s", params.BookID, refID),
		TerminalLabel:  refID,
		UUID:           params.PaymentID,
		Amount:         params.Amount,
		MerchantID:     "",
	}

	envCode, err := s.payment.GenQRCode(ctx, f)
	if err != nil {
		return "", err
	}

	return envCode, nil
}

func (s *PaymentService) handlePaymentNotification(params *jdb.Transaction) {
	ctx := context.Background()

	paymentKey := fmt.Sprintf("payment:%s", params.RefID)
	paymentData := s.Redis.HGetAll(ctx, paymentKey).Val()
	fmt.Printf("=> paymentData: %v\n", paymentData)

	userID := paymentData["user_id"]
	eventID := paymentData["event_id"]
	seatsJSON := paymentData["seats"]

	var seats []string
	json.Unmarshal([]byte(seatsJSON), &seats)

	// todo: use share seatService instance
	seatService := NewSeatService(s.Redis)
	for _, seatID := range seats {
		if err := seatService.MarkSeatAsSold(ctx, eventID, seatID, userID); err != nil {
			slog.Error("seatService.MarkSeatAsSold()", "error", err)
		}
		// todo: update seat status in sql
	}

	s.Redis.HSet(ctx, paymentKey, "status", "completed")
	// todo: update payment status in sql

	if err := s.queue.RemoveFromProcessing(ctx, eventID, userID); err != nil {
		slog.Error("payment.s.queue.RemoveFromProcessing()", "error", err)
	}

	// todo: check go context
	// go s.queue.ProcessQueue(ctx, eventID)
	s.queue.TriggerProcessQueue(eventID)

	channel := fmt.Sprintf("user-%s", userID)
	s.PubNub.Publish().
		Channel(channel).
		Message(map[string]any{
			"type":       "payment_success",
			"payment_id": params.RefID,
			"seats":      seats,
		}).
		Execute()
}

func (s *PaymentService) YesPay(ctx context.Context, uuid string) error {
	// check payment by uuid

	// check book

	// t := Transaction{
	// 	RefID:         tran.RefID,
	// 	UUID:          tran.UUID,
	// 	FCCRef:        tran.FCCRef,
	// 	Payer:         tran.Payer,
	// 	Amount:        tran.Amount,
	// 	Ccy:           tran.Ccy,
	// 	AccountNumber: tran.AccountNumber,
	// 	PayType:       PayTypeLAPNet,
	// 	Description:   couponCode,
	// 	CreatedAt:     tran.CreatedAt,
	// }
	// if err := s.r.Store(ctx, &t); err != nil {
	// 	return fmt.Errorf("s.r.Store: %w", err)
	// }

	// book.Status = domain.BookStatusSuccess
	// if !tran.Amount.Equal(book.Amount()) {
	// 	book.Status = domain.BookStatusDenied
	// }
	// book.UpdatedAt = domain.NowPtr()
	// book.IsSuccessPayment = true
	// if err := s.bookRepo.Update(ctx, book); err != nil {
	// 	return fmt.Errorf("s.bookRepo.Update: %w", err)
	// }

	// messagePayloadPayment := publishMessage{
	// 	UUID:    book.ID,
	// 	Code:    http.StatusOK,
	// 	Status:  "OK",
	// 	Message: messagePayment,
	// }
	// go func() {
	// 	result, err := s.publishMessage(ctx, book.ID, &messagePayloadPayment)
	// 	if err != nil {
	// 		log.Printf("Publish message payment failed: %v", err)
	// 		return
	// 	}
	// 	log.Println("Publish message payment successful:", result)
	// }()

	// result, err := s.sendNotification(ctx, book, notFree)
	// if err != nil {
	// 	log.Printf("Send notification failed: %v", err)
	// }
	// log.Printf("Send notification successful: %v", result)

	return nil
}
