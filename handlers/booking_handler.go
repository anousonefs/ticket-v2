package handlers

import (
	"fmt"
	"net/http"
	"ticket-system/services"

	"github.com/pocketbase/pocketbase"
	"github.com/pocketbase/pocketbase/apis"
	"github.com/pocketbase/pocketbase/core"
)

type BookingHandler struct {
	app            *pocketbase.PocketBase
	seatService    *services.SeatService
	paymentService *services.PaymentService
}

func NewBookingHandler(app *pocketbase.PocketBase, seatService *services.SeatService, paymentService *services.PaymentService) *BookingHandler {
	return &BookingHandler{
		app:            app,
		seatService:    seatService,
		paymentService: paymentService,
	}
}

// ConfirmBooking - Confirm seat selection and create payment
func (h *BookingHandler) ConfirmBooking(e *core.RequestEvent) error {
	if e.Auth == nil {
		return apis.NewUnauthorizedError("Unauthorized", nil)
	}

	var req struct {
		EventID string   `json:"event_id"`
		SeatIDs []string `json:"seat_ids"`
	}
	if err := e.BindBody(&req); err != nil {
		return apis.NewBadRequestError("Invalid request", err)
	}
	ctx := e.Request.Context()

	totalAmount := 0.0
	for _, seatID := range req.SeatIDs {
		seat, err := h.app.FindRecordById("seats", seatID)
		if err != nil {
			return apis.NewBadRequestError("Invalid seat", err)
		}
		seatKey := fmt.Sprintf("seat:%s:%s", req.EventID, seatID)
		lockedBy, _ := h.seatService.Redis.HGet(ctx, seatKey, "locked_by").Result()
		if lockedBy != e.Auth.Id {
			return apis.NewBadRequestError("Seat not locked by user", nil)
		}
		totalAmount += seat.GetFloat("price")
	}

	paymentID, err := h.paymentService.CreatePaymentSession(ctx, e.Auth.Id, req.EventID, req.SeatIDs, totalAmount)
	if err != nil {
		return apis.NewBadRequestError("Failed to create payment", err)
	}

	collection, err := h.app.FindCollectionByNameOrId("bookings")
	if err != nil {
		return apis.NewBadRequestError("Failed to get bookings collection", err)
	}
	booking := core.NewRecord(collection)
	booking.Set("user_id", e.Auth.Id)
	booking.Set("event_id", req.EventID)
	booking.Set("payment_id", paymentID)
	booking.Set("seats", req.SeatIDs)
	booking.Set("total_amount", totalAmount)
	booking.Set("status", "pending")
	if err := h.app.SaveWithContext(ctx, booking); err != nil {
		return apis.NewBadRequestError("Failed to create booking", err)
	}

	return e.JSON(http.StatusOK, map[string]any{
		"payment_id": paymentID,
		"amount":     totalAmount,
		"booking_id": booking.Id,
	})
}

// GetBookingHistory - Get user's booking history
func (h *BookingHandler) GetBookingHistory(e *core.RequestEvent) error {
	if e.Auth == nil {
		return apis.NewUnauthorizedError("Unauthorized", nil)
	}

	bookings, err := h.app.FindRecordsByFilter(
		"bookings",
		"user_id = {:userId}",
		"-created",
		20,
		0,
		map[string]any{"userId": e.Auth.Id},
	)
	if err != nil {
		return apis.NewBadRequestError("Failed to get bookings", err)
	}

	result := []map[string]any{}
	for _, booking := range bookings {
		event, _ := h.app.FindRecordById("events", booking.GetString("event_id"))
		result = append(result, map[string]any{
			"id":           booking.Id,
			"event_id":     booking.GetString("event_id"),
			"event_name":   event.GetString("name"),
			"seats":        booking.Get("seats"),
			"total_amount": booking.GetFloat("total_amount"),
			"status":       booking.GetString("status"),
			"created":      booking.GetDateTime("created"),
		})
	}

	return e.JSON(http.StatusOK, result)
}
