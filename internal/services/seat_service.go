package services

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/redis/go-redis/v9"
)

type SeatService struct {
	Redis *redis.Client
}

func NewSeatService(redisClient *redis.Client) *SeatService {
	return &SeatService{Redis: redisClient}
}

func (s *SeatService) LockSeatForUser(ctx context.Context, eventID, seatID, userID string) error {
	userSeatKey := fmt.Sprintf("seat:%s:%s", eventID, userID)

	// Atomic operation: Unlock previous seat and lock new seat
	err := s.Redis.Watch(ctx, func(tx *redis.Tx) error {
		// Get user's currently locked seat (if any)
		previousSeatData, err := tx.HGetAll(ctx, userSeatKey).Result()
		if err != nil && err != redis.Nil {
			return err
		}

		// Check if requested seat is available
		if err := s.checkSeatAvailability(ctx, tx, eventID, seatID, userID); err != nil {
			return err
		}

		// Atomic operation: unlock previous + lock new
		_, err = tx.TxPipelined(ctx, func(pipe redis.Pipeliner) error {
			// If user had a previous seat, unlock it first
			if len(previousSeatData) > 0 && previousSeatData["status"] == "locked" {
				pipe.Del(ctx, userSeatKey)
			}

			// Lock the new seat
			pipe.HSet(ctx, userSeatKey, map[string]interface{}{
				"seat_id":   seatID,
				"status":    "locked",
				"locked_by": userID,
				"locked_at": time.Now().Unix(),
			})
			pipe.Expire(ctx, userSeatKey, 5*time.Minute)

			return nil
		})

		return err
	}, userSeatKey)
	if err != nil {
		slog.Error("Failed to lock seat", "error", err, "seat_id", seatID, "user_id", userID)
		return err
	}

	return nil
}

// Helper method for checking seat availability
func (s *SeatService) checkSeatAvailability(ctx context.Context, tx *redis.Tx, eventID, seatID, userID string) error {
	allSeatsPattern := fmt.Sprintf("seat:%s:*", eventID)
	seatKeys, err := tx.Keys(ctx, allSeatsPattern).Result()
	if err != nil {
		return err
	}

	userSeatKey := fmt.Sprintf("seat:%s:%s", eventID, userID)

	// Check if any other user has locked this seat
	for _, key := range seatKeys {
		if key == userSeatKey {
			continue // Skip user's own key
		}

		seatData, _ := tx.HGetAll(ctx, key).Result()
		if seatData["seat_id"] == seatID && seatData["status"] == "locked" {
			return fmt.Errorf("seat already locked by another user")
		}
	}

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

type SeatData struct {
	ID      string  `db:"id" json:"id"`
	Row     string  `db:"row" json:"row"`
	Number  int     `db:"number" json:"number"`
	Section string  `db:"section" json:"section"`
	Price   float64 `db:"price" json:"price"`
	EventID string  `db:"event_id" json:"event_id"`
	Status  string  `json:"status"` // Will be populated from Redis
}
