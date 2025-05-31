package handlers

import (
	"fmt"
	"net/http"
	"ticket-system/services"

	"github.com/labstack/echo/v5"
	"github.com/pocketbase/pocketbase"
	"github.com/pocketbase/pocketbase/apis"
	pbmodels "github.com/pocketbase/pocketbase/models"
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
func (h *BookingHandler) ConfirmBooking(c echo.Context) error {
	authRecord, _ := c.Get(apis.ContextAuthRecordKey).(*pbmodels.Record)
	if authRecord == nil {
		return apis.NewUnauthorizedError("Unauthorized", nil)
	}

	var req struct {
		EventID string   `json:"event_id"`
		SeatIDs []string `json:"seat_ids"`
	}

	if err := c.Bind(&req); err != nil {
		return apis.NewBadRequestError("Invalid request", err)
	}

	ctx := c.Request().Context()

	// Verify all seats are locked by this user
	totalAmount := 0.0
	for _, seatID := range req.SeatIDs {
		// Get seat details
		seat, err := h.app.Dao().FindRecordById("seats", seatID)
		if err != nil {
			return apis.NewBadRequestError("Invalid seat", err)
		}

		// Verify lock
		seatKey := fmt.Sprintf("seat:%s:%s", req.EventID, seatID)
		lockedBy, _ := h.seatService.Redis.HGet(ctx, seatKey, "locked_by").Result()

		if lockedBy != authRecord.Id {
			return apis.NewBadRequestError("Seat not locked by user", nil)
		}

		totalAmount += seat.GetFloat("price")
	}

	// Create payment session
	paymentID, err := h.paymentService.CreatePaymentSession(ctx, authRecord.Id, req.EventID, req.SeatIDs, totalAmount)
	if err != nil {
		return apis.NewBadRequestError("Failed to create payment", err)
	}

	// Create booking record in database
	collection, _ := h.app.Dao().FindCollectionByNameOrId("bookings")
	booking := pbmodels.NewRecord(collection)
	booking.Set("user_id", authRecord.Id)
	booking.Set("event_id", req.EventID)
	booking.Set("payment_id", paymentID)
	booking.Set("seats", req.SeatIDs)
	booking.Set("total_amount", totalAmount)
	booking.Set("status", "pending")

	if err := h.app.Dao().SaveRecord(booking); err != nil {
		return apis.NewBadRequestError("Failed to create booking", err)
	}

	return c.JSON(http.StatusOK, map[string]any{
		"payment_id": paymentID,
		"amount":     totalAmount,
		"booking_id": booking.Id,
	})
}

// GetBookingHistory - Get user's booking history
func (h *BookingHandler) GetBookingHistory(c echo.Context) error {
	authRecord, _ := c.Get(apis.ContextAuthRecordKey).(*pbmodels.Record)
	if authRecord == nil {
		return apis.NewUnauthorizedError("Unauthorized", nil)
	}

	bookings, err := h.app.Dao().FindRecordsByFilter(
		"bookings",
		"user_id = {:userId}",
		"-created",
		20,
		0,
		map[string]any{"userId": authRecord.Id},
	)
	if err != nil {
		return apis.NewBadRequestError("Failed to get bookings", err)
	}

	// Expand event details
	result := []map[string]any{}
	for _, booking := range bookings {
		event, _ := h.app.Dao().FindRecordById("events", booking.GetString("event_id"))

		// if event != nil else "Unknown",

		bookingData := map[string]any{
			"id":           booking.Id,
			"event_id":     booking.GetString("event_id"),
			"event_name":   event.GetString("name"),
			"seats":        booking.Get("seats"),
			"total_amount": booking.GetFloat("total_amount"),
			"status":       booking.GetString("status"),
			"created":      booking.GetDateTime("created"),
		}

		result = append(result, bookingData)
	}

	return c.JSON(http.StatusOK, result)
}
