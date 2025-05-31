package models

import (
	"time"
)

type Ticket struct {
	ID        string     `json:"id"`
	EventID   string     `json:"event_id"`
	UserID    string     `json:"user_id"`
	SeatID    string     `json:"seat_id"`
	Price     float64    `json:"price"`
	Status    string     `json:"status"` // reserved, paid, cancelled
	CreatedAt time.Time  `json:"created_at"`
	PaidAt    *time.Time `json:"paid_at"`
}

type Seat struct {
	ID       string     `json:"id"`
	EventID  string     `json:"event_id"`
	Row      string     `json:"row"`
	Number   int        `json:"number"`
	Section  string     `json:"section"`
	Price    float64    `json:"price"`
	Status   string     `json:"status"` // available, locked, sold
	LockedBy string     `json:"locked_by,omitempty"`
	LockedAt *time.Time `json:"locked_at,omitempty"`
}
