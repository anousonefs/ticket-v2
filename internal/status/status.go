package status

import (
	"errors"
	"time"

	"github.com/shopspring/decimal"
)

var (
	ErrFailedPayment   = errors.New("payment: payment failed")
	ErrRefCodeNotFound = errors.New("ref code: ref code not found")
)

type FormQR struct {
	UUID           string
	Phone          string
	MerchantID     string
	ReferenceLabel string
	TerminalLabel  string
	Amount         decimal.Decimal
}

type Transaction struct {
	RefID         string
	UUID          string
	FCCRef        string
	Ccy           string
	Payer         string
	AccountNumber string
	Amount        decimal.Decimal
	CreatedAt     time.Time
}
