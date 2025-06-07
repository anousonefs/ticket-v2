package bank

import (
	"context"
	"ticket-system/internal/status"

	"github.com/shopspring/decimal"
)

// BankProvider represents different bank types
type BankProvider string

const (
	BankJDB  BankProvider = "jdb"
	BankLDB  BankProvider = "ldb"
	BankBCEL BankProvider = "bcel"
)

// PaymentRequest represents a generic payment request
type PaymentRequest struct {
	Amount          decimal.Decimal `json:"amount"`
	Currency        string          `json:"currency"`
	UUID            string          `json:"uuid"`
	ReferenceNumber string          `json:"reference_number"`
	Phone           string          `json:"phone"`
	MerchantID      string          `json:"merchant_id,omitempty"`
	ExpiryMinutes   string          `json:"expiry_minutes,omitempty"`
	Description     string          `json:"description,omitempty"`

	// Bank-specific fields
	TerminalLabel string `json:"terminal_label,omitempty"` // JDB specific
	IsDeepLink    bool   `json:"is_deep_link,omitempty"`   // LDB specific
}

// TransactionStatus represents transaction status
type TransactionStatus struct {
	UUID      string          `json:"uuid"`
	RefID     string          `json:"ref_id"`
	Status    string          `json:"status"`
	Amount    decimal.Decimal `json:"amount"`
	Currency  string          `json:"currency"`
	Timestamp int64           `json:"timestamp"`
}

// BankInterface defines the common interface for all bank payment providers
type BankInterface interface {
	// GetProvider returns the bank provider type
	GetProvider() BankProvider

	// GenerateQR generates a QR code for payment
	GenerateQR(ctx context.Context, req *PaymentRequest) (string, error)

	// CheckTransaction checks the status of a transaction
	CheckTransaction(ctx context.Context, uuid string) (*TransactionStatus, error)

	// SetTransactionChannel sets the channel for receiving transaction notifications
	SetTransactionChannel(ch chan *status.Transaction)

	// Close gracefully closes any connections
	Close(ctx context.Context) error
}

// BankFactory creates bank instances based on provider type
type BankFactory interface {
	CreateBank(ctx context.Context, provider BankProvider, config interface{}) (BankInterface, error)
	GetSupportedProviders() []BankProvider
}

