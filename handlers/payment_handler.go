package handlers

import (
   "fmt"
   "net/http"
   "ticket-system/services"

   "github.com/pocketbase/pocketbase"
   "github.com/pocketbase/pocketbase/apis"
   "github.com/pocketbase/pocketbase/core"
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
