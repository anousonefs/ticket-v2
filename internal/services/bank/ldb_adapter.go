package bank

import (
	"context"
	"fmt"
	"ticket-system/internal/services/bank/ldb"
	"ticket-system/internal/status"

	"github.com/google/uuid"
)

// LDBAdapter wraps the existing LDB implementation to conform to BankInterface
type LDBAdapter struct {
	client ldb.LDB
}

// NewLDBAdapter creates a new LDB adapter
func NewLDBAdapter(ctx context.Context, config *ldb.Config) (*LDBAdapter, error) {
	client, err := ldb.New(ctx, config)
	if err != nil {
		return nil, fmt.Errorf("failed to create LDB client: %w", err)
	}

	return &LDBAdapter{
		client: client,
	}, nil
}

// GetProvider returns the bank provider type
func (l *LDBAdapter) GetProvider() BankProvider {
	return BankLDB
}

// GenerateQR generates a QR code for payment using LDB
func (l *LDBAdapter) GenerateQR(ctx context.Context, req *PaymentRequest) (string, error) {
	expiryTime := req.ExpiryMinutes
	if expiryTime == "" {
		expiryTime = "5" // Default 5 minutes
	}

	reqTxUUID := uuid.New().String()

	form := &ldb.LDBQRForm{
		ExpiryTime:      expiryTime,
		TxCount:         "1",
		Amount:          req.Amount,
		Currency:        req.Currency,
		UUID:            req.UUID,
		ReferenceNumber: req.ReferenceNumber,
		MobileNumber:    req.Phone,
		Memo:            req.Description,
		IsDeepLink:      req.IsDeepLink,
		MerchantID:      req.MerchantID,
		ReqTxUUID:       reqTxUUID, // Use same UUID for request transaction
	}

	return l.client.GenQRCode(ctx, form)
}

// CheckTransaction checks the status of a transaction
func (l *LDBAdapter) CheckTransaction(ctx context.Context, uuid string) (*TransactionStatus, error) {
	// LDB requires both refID2 and reqTxUUID - use uuid for both
	tx, err := l.client.CheckTransaction(ctx, uuid, uuid)
	if err != nil {
		return nil, err
	}

	return &TransactionStatus{
		UUID:      uuid,
		RefID:     tx.RefID,
		Status:    "status",
		Amount:    tx.Amount,
		Currency:  tx.Ccy,
		Timestamp: tx.CreatedAt.Unix(),
	}, nil
}

// SetTransactionChannel sets the channel for receiving transaction notifications
// Note: LDB doesn't have built-in notification system like JDB
func (l *LDBAdapter) SetTransactionChannel(ch chan *status.Transaction) {
	// LDB doesn't support real-time notifications
	// This would need to be implemented via polling or webhook
}

// Close gracefully closes any connections
func (l *LDBAdapter) Close(ctx context.Context) error {
	// LDB doesn't have explicit close method
	return nil
}

