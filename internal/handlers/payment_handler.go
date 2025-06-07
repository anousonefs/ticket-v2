package handlers

import (
	"bytes"
	"fmt"
	"io"
	"log"
	"log/slog"
	"net/http"
	"ticket-system/internal/services"

	"github.com/pocketbase/pocketbase"
	"github.com/pocketbase/pocketbase/apis"
	"github.com/pocketbase/pocketbase/core"
	"github.com/shopspring/decimal"
)

type PaymentHandler struct {
	app            *pocketbase.PocketBase
	paymentService *services.PaymentService
}

func NewPaymentHandler(app *pocketbase.PocketBase, paymentService *services.PaymentService) *PaymentHandler {
	return &PaymentHandler{
		app:            app,
		paymentService: paymentService,
	}
}

// GetPaymentDetails - Get payment details
func (h *PaymentHandler) GetPaymentDetails(e *core.RequestEvent) error {
	if e.Auth == nil {
		return apis.NewUnauthorizedError("Unauthorized", nil)
	}

	paymentID := e.Request.PathValue("paymentId")
	ctx := e.Request.Context()

	paymentKey := fmt.Sprintf("payment:%s", paymentID)
	paymentData := h.paymentService.Redis.HGetAll(ctx, paymentKey).Val()

	if len(paymentData) == 0 {
		return apis.NewNotFoundError("Payment not found", nil)
	}

	if paymentData["user_id"] != e.Auth.Id {
		return apis.NewForbiddenError("Access denied", nil)
	}

	qrData := fmt.Sprintf(`{"payment_id":"%s","amount":%s,"bank_account":"1234567890","reference":"%s"}`,
		paymentID, paymentData["amount"], paymentID)

	return e.JSON(http.StatusOK, map[string]any{
		"payment_id": paymentID,
		"amount":     paymentData["amount"],
		"seats":      paymentData["seats"],
		"status":     paymentData["status"],
		"qr_code":    qrData,
		"expires_at": paymentData["expires_at"],
	})
}

// CheckPaymentStatus - Check payment status
func (h *PaymentHandler) CheckPaymentStatus(e *core.RequestEvent) error {
	if e.Auth == nil {
		return apis.NewUnauthorizedError("Unauthorized", nil)
	}

	paymentID := e.Request.PathValue("paymentId")
	ctx := e.Request.Context()

	paymentKey := fmt.Sprintf("payment:%s", paymentID)
	status, err := h.paymentService.Redis.HGet(ctx, paymentKey, "status").Result()
	if err != nil {
		return apis.NewNotFoundError("Payment not found", nil)
	}

	return e.JSON(http.StatusOK, map[string]any{"status": status})
}

// CancelPayment - Cancel payment and return to seat selection
func (h *PaymentHandler) CancelPayment(e *core.RequestEvent) error {
	if e.Auth == nil {
		return apis.NewUnauthorizedError("Unauthorized", nil)
	}

	paymentID := e.Request.PathValue("paymentId")
	ctx := e.Request.Context()

	paymentKey := fmt.Sprintf("payment:%s", paymentID)
	paymentData := h.paymentService.Redis.HGetAll(ctx, paymentKey).Val()

	if paymentData["user_id"] != e.Auth.Id {
		return apis.NewForbiddenError("Access denied", nil)
	}

	if paymentData["status"] == "completed" {
		return apis.NewBadRequestError("Cannot cancel completed payment", nil)
	}

	bookings, _ := h.app.FindRecordsByFilter(
		"bookings",
		"payment_id = {:paymentId}",
		"",
		1,
		0,
		map[string]any{"paymentId": paymentID},
	)
	if len(bookings) > 0 {
		b := bookings[0]
		b.Set("status", "cancelled")
		h.app.SaveWithContext(ctx, b)
	}

	return e.JSON(http.StatusOK, map[string]any{"message": "Payment cancelled"})
}

// SimulatePayment - Simulate payment success (for testing)
func (h *PaymentHandler) SimulatePayment(e *core.RequestEvent) error {
	var req struct {
		PaymentID string `json:"payment_id"`
		Status    string `json:"status"`
	}
	if err := e.BindBody(&req); err != nil {
		return apis.NewBadRequestError("Invalid request", err)
	}

	h.paymentService.PubNub.Publish().
		Channel("bank-payment-notifications").
		Message(map[string]any{"payment_id": req.PaymentID, "status": req.Status}).
		Execute()

	return e.JSON(http.StatusOK, map[string]any{"message": "Payment simulation sent"})
}

func (h *PaymentHandler) GenQR(e *core.RequestEvent) error {
	var req services.QRRequest
	if err := e.BindBody(&req); err != nil {
		return apis.NewBadRequestError("Invalid request", err)
	}
	ctx := e.Request.Context()
	code, err := h.paymentService.GenQR(ctx, req)
	if err != nil {
		slog.Error("h.paymentService.GenQR()", "req", req, "error", err)
		return apis.NewInternalServerError("internal error", err)
	}
	return e.JSON(http.StatusOK, map[string]any{"status": "success", "code": code})
}

type LDBHookReq struct {
	NotifyID int64           `json:"notifyId"`
	Status   string          `json:"processingStatus"`
	UUID     string          `json:"partnerOrderID"`
	RefID2   string          `json:"partnerPaymentID"`
	Bank     string          `json:"paymentBank"`
	Time     string          `json:"paymentAt"`
	TxID     string          `json:"paymentReference"`
	Amount   decimal.Decimal `json:"amount"`
	Ccy      string          `json:"currency"`
}

func (h *PaymentHandler) LDBConfirmationPayment(e *core.RequestEvent) error {
	r := e.Request
	rClone := r.Clone(r.Context())

	var b bytes.Buffer
	b.ReadFrom(r.Body)
	r.Body = io.NopCloser(&b)

	// Update cloned request body
	rClone.Body = io.NopCloser(bytes.NewReader(b.Bytes()))

	// Read cloned request body
	rBody, _ := io.ReadAll(rClone.Body)
	slog.Info("=> LDBConfirmationPayment", "read Body", rBody)

	var req LDBHookReq
	if err := e.BindBody(&req); err != nil {
		return e.JSON(http.StatusBadRequest, "bad request")
	}
	fmt.Printf("req: %+v\n", req)

	if req.UUID == "" || req.TxID == "" || req.Status != "FNLD" {
		log.Printf("LDBConfirmationPayment: req.UUID: %s, req.TxID: %s, req.Status: %s", req.UUID, req.TxID, req.Status)
		return e.JSON(http.StatusBadRequest, "invalid hook request body")
	}

	return e.JSON(http.StatusOK, map[string]any{
		"code":    200,
		"status":  "OK",
		"message": "LDB Confirmation payment successful.",
	})
}
