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
	redis  *redis.Client
	pubnub *pubnub.PubNub
	queue  *QueueService
}

func NewPaymentService(redisClient *redis.Client, pn *pubnub.PubNub, queueService *QueueService) *PaymentService {
	service := &PaymentService{
		redis:  redisClient,
		pubnub: pn,
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
		s.redis.HSet(ctx, paymentKey, k, v)
	}

	s.redis.Expire(ctx, paymentKey, 10*time.Minute)

	return paymentID, nil
}

func (s *PaymentService) SubscribeToPaymentNotifications() {
	listener := pubnub.NewListener()

	s.pubnub.AddListener(listener)
	s.pubnub.Subscribe().
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

	data, ok := message.Message.(map[string]interface{})
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
		paymentData := s.redis.HGetAll(ctx, paymentKey).Val()

		userID := paymentData["user_id"]
		eventID := paymentData["event_id"]
		seatsJSON := paymentData["seats"]

		var seats []string
		json.Unmarshal([]byte(seatsJSON), &seats)

		seatService := NewSeatService(s.redis)
		for _, seatID := range seats {
			seatService.MarkSeatAsSold(ctx, eventID, seatID, userID)
		}

		s.redis.HSet(ctx, paymentKey, "status", "completed")

		s.queue.RemoveFromProcessing(ctx, eventID, userID)

		go s.queue.ProcessQueue(ctx, eventID)

		channel := fmt.Sprintf("user-%s", userID)
		s.pubnub.Publish().
			Channel(channel).
			Message(map[string]interface{}{
				"type":       "payment_success",
				"payment_id": notification.PaymentID,
				"seats":      seats,
			}).
			Execute()
	}
}
