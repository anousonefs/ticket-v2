package models

import (
	"time"
)

type Event struct {
	ID          string    `json:"id"`
	Name        string    `json:"name"`
	Description string    `json:"description"`
	Venue       string    `json:"venue"`
	StartTime   time.Time `json:"start_time"`
	EndTime     time.Time `json:"end_time"`
	TotalSeats  int       `json:"total_seats"`
	Status      string    `json:"status"` // upcoming, ongoing, completed
}
