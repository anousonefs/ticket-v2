package models

import (
	"time"
)

type QueueEntry struct {
	UserID    string    `json:"user_id"`
	EventID   string    `json:"event_id"`
	JoinedAt  time.Time `json:"joined_at"`
	Position  int       `json:"position"`
	Status    string    `json:"status"` // waiting, processing, completed
	SessionID string    `json:"session_id"`
}

type ProcessingUser struct {
	UserID      string    `json:"user_id"`
	EventID     string    `json:"event_id"`
	StartedAt   time.Time `json:"started_at"`
	LockedSeats []string  `json:"locked_seats"`
	SessionID   string    `json:"session_id"`
}

type QueueMetrics struct {
	EventID         string    `json:"event_id"`
	TotalInQueue    int       `json:"total_in_queue"`
	ProcessingCount int       `json:"processing_count"`
	AvgWaitTime     float64   `json:"avg_wait_time"`
	LastUpdated     time.Time `json:"last_updated"`
}
