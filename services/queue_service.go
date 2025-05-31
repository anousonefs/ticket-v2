package services

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"ticket-system/config"
	"ticket-system/models"
	"time"

	pubnub "github.com/pubnub/go"
	"github.com/redis/go-redis/v9"
)

type QueueService struct {
	Redis  *redis.Client
	pubnub *pubnub.PubNub
	config *config.Config
}

func NewQueueService(redisClient *redis.Client, pn *pubnub.PubNub, cfg *config.Config) *QueueService {
	return &QueueService{
		Redis:  redisClient,
		pubnub: pn,
		config: cfg,
	}
}

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
		return err
	}

	queueKey := fmt.Sprintf("queue:waiting:%s", eventID)

	queueLen, err := s.Redis.LLen(ctx, queueKey).Result()
	if err != nil {
		return err
	}

	if err := s.Redis.LPush(ctx, queueKey, data).Err(); err != nil {
		return err
	}

	userKey := fmt.Sprintf("user:queue:%s:%s", eventID, userID)
	s.Redis.HSet(ctx, userKey, map[string]any{
		"status":     "waiting",
		"joined_at":  time.Now().Unix(),
		"session_id": sessionID,
	})

	if queueLen == 0 {
		go s.ProcessQueue(ctx, eventID)
	}

	return nil
}

func (s *QueueService) ProcessQueue(ctx context.Context, eventID string) {
	processingKey := fmt.Sprintf("queue:processing:%s", eventID)
	waitingKey := fmt.Sprintf("queue:waiting:%s", eventID)

	for {
		processingCount, err := s.Redis.SCard(ctx, processingKey).Result()
		if err != nil {
			log.Printf("Error getting processing count: %v", err)
			break
		}

		if processingCount >= int64(s.config.MaxProcessingUsers) {
			break
		}

		data, err := s.Redis.RPop(ctx, waitingKey).Result()
		if err == redis.Nil {
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

		processingUser := models.ProcessingUser{
			UserID:    entry.UserID,
			EventID:   entry.EventID,
			StartedAt: time.Now(),
			SessionID: entry.SessionID,
		}

		processingData, _ := json.Marshal(processingUser)
		s.Redis.SAdd(ctx, processingKey, processingData)

		userKey := fmt.Sprintf("user:queue:%s:%s", eventID, entry.UserID)
		s.Redis.HSet(ctx, userKey, "status", "processing")

		channel := fmt.Sprintf("user-%s", entry.UserID)
		s.pubnub.Publish().
			Channel(channel).
			Message(map[string]any{
				"type":     "queue_status",
				"status":   "processing",
				"event_id": eventID,
			}).
			Execute()

		// todo: fix handle many goroutine, what about go context
		go s.setProcessingTimeout(ctx, eventID, entry.UserID)
	}
}

func (s *QueueService) setProcessingTimeout(ctx context.Context, eventID, userID string) {
	time.Sleep(s.config.SeatLockTimeout)

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

func (s *QueueService) RemoveFromProcessing(ctx context.Context, eventID, userID string) error {
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

func (s *QueueService) UpdateQueuePositions(ctx context.Context) {
	ticker := time.NewTicker(s.config.QueuePositionUpdate)
	defer ticker.Stop()

	for range ticker.C {
		keys, err := s.Redis.Keys(ctx, "queue:waiting:*").Result()
		if err != nil {
			log.Printf("Error getting queue keys: %v", err)
			continue
		}

		fmt.Printf("---------- keys: %+v ----------------------\n", keys)
		for _, key := range keys {
			eventID := key[len("queue:waiting:"):]
			fmt.Printf("=> UpdateQueuePositions eventID: %v, key: %v\n", eventID, key)

			entries, err := s.Redis.LRange(ctx, key, 0, -1).Result()
			if err != nil {
				continue
			}

			for i, entryData := range entries {
				var entry models.QueueEntry
				if err := json.Unmarshal([]byte(entryData), &entry); err != nil {
					continue
				}

				// todo: position should be i + 1
				position := len(entries) - i

				posKey := fmt.Sprintf("queue:position:%s:%s", eventID, entry.UserID)
				s.Redis.Set(ctx, posKey, position, 5*time.Second)

				channel := fmt.Sprintf("user-%s", entry.UserID)
				s.pubnub.Publish().
					Channel(channel).
					Message(map[string]any{
						"type":     "queue_position",
						"position": position,
						"event_id": eventID,
					}).
					Execute()
			}
		}
	}
}
