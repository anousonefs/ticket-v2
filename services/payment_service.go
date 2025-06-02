package services

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"time"

	pubnub "github.com/pubnub/go"
	"github.com/redis/go-redis/v9"
)

type PaymentService struct {
	Redis  *redis.Client
	PubNub *pubnub.PubNub
	queue  *QueueService
}

func NewPaymentService(redisClient *redis.Client, pn *pubnub.PubNub, queueService *QueueService) *PaymentService {
	service := &PaymentService{
		Redis:  redisClient,
		PubNub: pn,
		queue:  queueService,
	}

	go service.SubscribeToPaymentNotifications()

	return service
}

func (s *PaymentService) CreatePaymentSession(ctx context.Context, userID, eventID string, seats []string, amount float64) (string, error) {
	paymentID := fmt.Sprintf("payment_%s_%d", userID, time.Now().Unix())

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
	listener := pubnub.NewListener()

	s.PubNub.AddListener(listener)
	s.PubNub.Subscribe().
		Channels([]string{"bank-payment-notifications"}).
		Execute()

	for {
		select {
		case message := <-listener.Message:
			go s.handlePaymentNotification(message)
		}
	}
}

func (s *PaymentService) handlePaymentNotification(message *pubnub.PNMessage) {
	var notification struct {
		PaymentID string `json:"payment_id"`
		Status    string `json:"status"`
	}

	data, ok := message.Message.(map[string]any)
	if !ok {
		return
	}

	jsonData, _ := json.Marshal(data)
	if err := json.Unmarshal(jsonData, &notification); err != nil {
		log.Printf("Error parsing payment notification: %v", err)
		return
	}

	ctx := context.Background()

	if notification.Status == "success" {
		paymentKey := fmt.Sprintf("payment:%s", notification.PaymentID)
		paymentData := s.Redis.HGetAll(ctx, paymentKey).Val()

		userID := paymentData["user_id"]
		eventID := paymentData["event_id"]
		seatsJSON := paymentData["seats"]

		var seats []string
		json.Unmarshal([]byte(seatsJSON), &seats)

		// todo: use share seatService instance
		seatService := NewSeatService(s.Redis)
		for _, seatID := range seats {
			seatService.MarkSeatAsSold(ctx, eventID, seatID, userID)
			// todo: update seat status in sql
		}

		s.Redis.HSet(ctx, paymentKey, "status", "completed")
		// todo: update payment status in sql

		s.queue.RemoveFromProcessing(ctx, eventID, userID)

		// todo: check go context
		// go s.queue.ProcessQueue(ctx, eventID)
		s.queue.TriggerProcessQueue(eventID)

		channel := fmt.Sprintf("user-%s", userID)
		s.PubNub.Publish().
			Channel(channel).
			Message(map[string]any{
				"type":       "payment_success",
				"payment_id": notification.PaymentID,
				"seats":      seats,
			}).
			Execute()
	}
}
