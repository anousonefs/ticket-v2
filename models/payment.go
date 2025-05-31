package models

import (
	"time"
)

type Payment struct {
	ID            string     `json:"payment_id"`
	UserID        string     `json:"user_id"`
	EventID       string     `json:"event_id"`
	Seats         []string   `json:"seats"`
	Amount        float64    `json:"amount"`
	Status        string     `json:"status"`         // pending, processing, completed, failed
	PaymentMethod string     `json:"payment_method"` // qr_code, credit_card, bank_transfer
	QRCode        string     `json:"qr_code,omitempty"`
	CreatedAt     time.Time  `json:"created_at"`
	ExpiresAt     time.Time  `json:"expires_at"`
	CompletedAt   *time.Time `json:"completed_at,omitempty"`
}

type PaymentNotification struct {
	PaymentID     string    `json:"payment_id"`
	Status        string    `json:"status"`
	TransactionID string    `json:"transaction_id"`
	Timestamp     time.Time `json:"timestamp"`
}
