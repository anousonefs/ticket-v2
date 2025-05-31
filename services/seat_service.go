package services

import (
	"context"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
)

type SeatService struct {
	Redis *redis.Client
}

func NewSeatService(redisClient *redis.Client) *SeatService {
	return &SeatService{Redis: redisClient}
}

func (s *SeatService) LockSeat(ctx context.Context, eventID, seatID, userID string) error {
	seatKey := fmt.Sprintf("seat:%s:%s", eventID, seatID)

	status, err := s.Redis.HGet(ctx, seatKey, "status").Result()
	if err != nil && err != redis.Nil {
		return err
	}

	if status == "locked" || status == "sold" {
		return fmt.Errorf("seat not available")
	}

	s.Redis.HSet(ctx, seatKey, map[string]any{
		"status":    "locked",
		"locked_by": userID,
		"locked_at": time.Now().Unix(),
	})

	s.Redis.Expire(ctx, seatKey, 5*time.Minute)

	userProcessingKey := fmt.Sprintf("user:processing:%s:%s", eventID, userID)
	s.Redis.SAdd(ctx, userProcessingKey, seatID)

	return nil
}

func (s *SeatService) UnlockSeat(ctx context.Context, eventID, seatID string) error {
	seatKey := fmt.Sprintf("seat:%s:%s", eventID, seatID)

	status, _ := s.Redis.HGet(ctx, seatKey, "status").Result()
	if status == "sold" {
		return fmt.Errorf("cannot unlock sold seat")
	}

	s.Redis.Del(ctx, seatKey)
	return nil
}

func (s *SeatService) MarkSeatAsSold(ctx context.Context, eventID, seatID, userID string) error {
	seatKey := fmt.Sprintf("seat:%s:%s", eventID, seatID)

	s.Redis.HSet(ctx, seatKey, map[string]any{
		"status":  "sold",
		"sold_to": userID,
		"sold_at": time.Now().Unix(),
	})

	s.Redis.Persist(ctx, seatKey)

	return nil
}

func (s *SeatService) GetSeatAvailability(ctx context.Context, eventID string, seatIDs []string) (map[string]string, error) {
	availability := make(map[string]string)

	for _, seatID := range seatIDs {
		seatKey := fmt.Sprintf("seat:%s:%s", eventID, seatID)
		status, err := s.Redis.HGet(ctx, seatKey, "status").Result()

		if err == redis.Nil {
			availability[seatID] = "available"
		} else if err != nil {
			return nil, err
		} else {
			availability[seatID] = status
		}
	}

	return availability, nil
}
