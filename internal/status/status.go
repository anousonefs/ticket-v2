package status

import "errors"

var (
	ErrFailedPayment   = errors.New("payment: payment failed")
	ErrRefCodeNotFound = errors.New("ref code: ref code not found")
)
