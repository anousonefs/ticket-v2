package handlers

import (
	"fmt"
	"net/http"
	"ticket-system/services"

	"github.com/labstack/echo/v5"
	"github.com/pocketbase/pocketbase"
	"github.com/pocketbase/pocketbase/apis"
	"github.com/pocketbase/pocketbase/models"
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
func (h *PaymentHandler) GetPaymentDetails(c echo.Context) error {
	authRecord, _ := c.Get(apis.ContextAuthRecordKey).(*models.Record)
	if authRecord == nil {
		return apis.NewUnauthorizedError("Unauthorized", nil)
	}

	paymentID := c.PathParam("paymentId")
	ctx := c.Request().Context()

	// Get payment details from Redis
	paymentKey := fmt.Sprintf("payment:%s", paymentID)
	paymentData := h.paymentService.Redis.HGetAll(ctx, paymentKey).Val()

	if len(paymentData) == 0 {
		return apis.NewNotFoundError("Payment not found", nil)
	}

	// Verify payment belongs to user
	if paymentData["user_id"] != authRecord.Id {
		return apis.NewForbiddenError("Access denied", nil)
	}

	// Generate QR code data
	qrData := fmt.Sprintf(`{
		"payment_id": "%s",
		"amount": %s,
		"bank_account": "1234567890",
		"reference": "%s"
	}`, paymentID, paymentData["amount"], paymentID)

	return c.JSON(http.StatusOK, map[string]any{
		"payment_id": paymentID,
		"amount":     paymentData["amount"],
		"seats":      paymentData["seats"],
		"status":     paymentData["status"],
		"qr_code":    qrData,
		"expires_at": paymentData["expires_at"],
	})
}

// CheckPaymentStatus - Check payment status
func (h *PaymentHandler) CheckPaymentStatus(c echo.Context) error {
	authRecord, _ := c.Get(apis.ContextAuthRecordKey).(*models.Record)
	if authRecord == nil {
		return apis.NewUnauthorizedError("Unauthorized", nil)
	}

	paymentID := c.PathParam("paymentId")
	ctx := c.Request().Context()

	paymentKey := fmt.Sprintf("payment:%s", paymentID)
	status, err := h.paymentService.Redis.HGet(ctx, paymentKey, "status").Result()
	if err != nil {
		return apis.NewNotFoundError("Payment not found", nil)
	}

	return c.JSON(http.StatusOK, map[string]any{
		"status": status,
	})
}

// CancelPayment - Cancel payment and return to seat selection
func (h *PaymentHandler) CancelPayment(c echo.Context) error {
	authRecord, _ := c.Get(apis.ContextAuthRecordKey).(*models.Record)
	if authRecord == nil {
		return apis.NewUnauthorizedError("Unauthorized", nil)
	}

	paymentID := c.PathParam("paymentId")
	ctx := c.Request().Context()

	// Get payment details
	paymentKey := fmt.Sprintf("payment:%s", paymentID)
	paymentData := h.paymentService.Redis.HGetAll(ctx, paymentKey).Val()

	if paymentData["user_id"] != authRecord.Id {
		return apis.NewForbiddenError("Access denied", nil)
	}

	if paymentData["status"] == "completed" {
		return apis.NewBadRequestError("Cannot cancel completed payment", nil)
	}

	// Cancel payment
	// h.paymentService.CancelPayment(ctx, paymentID)

	// Update booking status
	bookings, _ := h.app.Dao().FindRecordsByFilter(
		"bookings",
		"payment_id = {:paymentId}",
		"",
		1,
		0,
		map[string]any{"paymentId": paymentID},
	)

	if len(bookings) > 0 {
		booking := bookings[0]
		booking.Set("status", "cancelled")
		h.app.Dao().SaveRecord(booking)
	}

	return c.JSON(http.StatusOK, map[string]any{
		"message": "Payment cancelled",
	})
}

// SimulatePayment - Simulate payment success (for testing)
func (h *PaymentHandler) SimulatePayment(c echo.Context) error {
	var req struct {
		PaymentID string `json:"payment_id"`
		Status    string `json:"status"`
	}

	if err := c.Bind(&req); err != nil {
		return apis.NewBadRequestError("Invalid request", err)
	}

	// Publish payment notification
	h.paymentService.PubNub.Publish().
		Channel("bank-payment-notifications").
		Message(map[string]any{
			"payment_id": req.PaymentID,
			"status":     req.Status,
		}).
		Execute()

	return c.JSON(http.StatusOK, map[string]any{
		"message": "Payment simulation sent",
	})
}
