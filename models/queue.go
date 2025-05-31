package models

import (
    "time"
)

type QueueEntry struct {
    UserID      string    `json:"user_id"`
    EventID     string    `json:"event_id"`
    JoinedAt    time.Time `json:"joined_at"`
    Position    int       `json:"position"`
    Status      string    `json:"status"`
    SessionID   string    `json:"session_id"`
}

type ProcessingUser struct {
    UserID      string    `json:"user_id"`
    EventID     string    `json:"event_id"`
    StartedAt   time.Time `json:"started_at"`
    LockedSeats []string  `json:"locked_seats"`
    SessionID   string    `json:"session_id"`
}
