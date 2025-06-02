package handlers

import (
	"fmt"
	"net/http"
	"ticket-system/services"

	"github.com/pocketbase/pocketbase"
	"github.com/pocketbase/pocketbase/apis"
	"github.com/pocketbase/pocketbase/core"
)

type SeatHandler struct {
	app         *pocketbase.PocketBase
	seatService *services.SeatService
}

func NewSeatHandler(app *pocketbase.PocketBase, seatService *services.SeatService) *SeatHandler {
	return &SeatHandler{
		app:         app,
		seatService: seatService,
	}
}

// GetSeats - Get all seats for an event
func (h *SeatHandler) GetSeats(e *core.RequestEvent) error {
	eventID := e.Request.PathValue("eventId")

	seats, err := h.app.FindRecordsByFilter(
		"seats",
		"event_id = {:eventId}",
		"-row",
		-1,
		0,
		map[string]any{"eventId": eventID},
	)
	if err != nil {
		return apis.NewBadRequestError("Failed to get seats", err)
	}

	seatIDs := make([]string, len(seats))
	for i, seat := range seats {
		seatIDs[i] = seat.Id
	}
	availability, err := h.seatService.GetSeatAvailability(e.Request.Context(), eventID, seatIDs)
	if err != nil {
		return apis.NewBadRequestError("Failed to get availability", err)
	}

	sections := make(map[string][]map[string]any)
	for _, seat := range seats {
		seatData := map[string]any{
			"id":      seat.Id,
			"row":     seat.GetString("row"),
			"number":  seat.GetInt("number"),
			"section": seat.GetString("section"),
			"price":   seat.GetFloat("price"),
			"status":  availability[seat.Id],
		}

		section := seat.GetString("section")
		sections[section] = append(sections[section], seatData)
		sections[seat.GetString("section")] = append(sections[seat.GetString("section")], seatData)
	}

	return e.JSON(http.StatusOK, map[string]any{
		"sections":        sections,
		"total_seats":     len(seats),
		"available_seats": countAvailable(availability),
	})
}

// LockSeatsBatch - Lock multiple seats
func (h *SeatHandler) LockSeatsBatch(e *core.RequestEvent) error {
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

	lockedSeats := []string{}
	for _, seatID := range req.SeatIDs {
		if err := h.seatService.LockSeat(ctx, req.EventID, seatID, e.Auth.Id); err == nil {
			lockedSeats = append(lockedSeats, seatID)
		}
	}
	return e.JSON(http.StatusOK, map[string]any{"locked_seats": lockedSeats, "failed_count": len(req.SeatIDs) - len(lockedSeats)})
}

// UnlockSeatsBatch - Unlock multiple seats
func (h *SeatHandler) UnlockSeatsBatch(e *core.RequestEvent) error {
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

	unlockedSeats := []string{}
	for _, seatID := range req.SeatIDs {
		seatKey := fmt.Sprintf("seat:%s:%s", req.EventID, seatID)
		lockedBy, _ := h.seatService.Redis.HGet(ctx, seatKey, "locked_by").Result()
		if lockedBy == e.Auth.Id {
			if err := h.seatService.UnlockSeat(ctx, req.EventID, seatID); err == nil {
				unlockedSeats = append(unlockedSeats, seatID)
			}
		}
	}
	return e.JSON(http.StatusOK, map[string]any{"unlocked_seats": unlockedSeats})
}

func countAvailable(availability map[string]string) int {
	count := 0
	for _, status := range availability {
		if status == "available" {
			count++
		}
	}
	return count
}
