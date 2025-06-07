package bank

import (
	"context"
	"fmt"
	"ticket-system/internal/services"
	"ticket-system/internal/services/bank/jdb"
	"ticket-system/internal/status"

	"github.com/shopspring/decimal"
)

// JDBAdapter wraps the existing JDB implementation to conform to BankInterface
type JDBAdapter struct {
	client *jdb.Yespay
}

// NewJDBAdapter creates a new JDB adapter
func NewJDBAdapter(ctx context.Context, config *jdb.Config) (*JDBAdapter, error) {
	client, err := jdb.New(ctx, config)
	if err != nil {
		return nil, fmt.Errorf("failed to create JDB client: %w", err)
	}

	return &JDBAdapter{
		client: client,
	}, nil
}

// GetProvider returns the bank provider type
func (j *JDBAdapter) GetProvider() services.BankProvider {
	return services.BankJDB
}

// GenerateQR generates a QR code for payment using JDB
func (j *JDBAdapter) GenerateQR(ctx context.Context, req *services.PaymentRequest) (string, error) {
	form := &status.FormQR{
		Phone:          req.Phone,
		ReferenceLabel: req.ReferenceNumber,
		TerminalLabel:  req.TerminalLabel,
		UUID:           req.UUID,
		Amount:         req.Amount,
		MerchantID:     req.MerchantID,
	}

	return j.client.GenQRCode(ctx, form)
}

// CheckTransaction checks the status of a transaction
func (j *JDBAdapter) CheckTransaction(ctx context.Context, uuid string) (*services.TransactionStatus, error) {
	tx, err := j.client.CheckTransaction(ctx, uuid)
	if err != nil {
		return nil, err
	}

	return &services.TransactionStatus{
		UUID:      tx.UUID,
		RefID:     tx.RefID,
		Status:    "completed", // JDB doesn't return status, assume completed if found
		Amount:    tx.Amount,
		Currency:  tx.Ccy,
		Timestamp: tx.CreatedAt.Unix(),
	}, nil
}

// SetTransactionChannel sets the channel for receiving transaction notifications
func (j *JDBAdapter) SetTransactionChannel(ch chan *status.Transaction) {
	j.client.SetTranChannel(ch)
}

// Close gracefully closes any connections
func (j *JDBAdapter) Close(ctx context.Context) error {
	// JDB doesn't have explicit close method
	return nil
}