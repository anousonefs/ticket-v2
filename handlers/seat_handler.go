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
func (h *SeatHandler) GetSeats(c echo.Context) error {
	eventID := c.PathParam("eventId")

	// Get seats from database
	seats, err := h.app.Dao().FindRecordsByFilter(
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

	// Get seat availability from Redis
	seatIDs := make([]string, len(seats))
	for i, seat := range seats {
		seatIDs[i] = seat.Id
	}

	availability, err := h.seatService.GetSeatAvailability(c.Request().Context(), eventID, seatIDs)
	if err != nil {
		return apis.NewBadRequestError("Failed to get availability", err)
	}

	// Organize seats by section
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
	}

	return c.JSON(http.StatusOK, map[string]any{
		"sections":        sections,
		"total_seats":     len(seats),
		"available_seats": countAvailable(availability),
	})
}

// LockSeatsBatch - Lock multiple seats
func (h *SeatHandler) LockSeatsBatch(c echo.Context) error {
	authRecord, _ := c.Get(apis.ContextAuthRecordKey).(*models.Record)
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

	// Lock seats
	lockedSeats := []string{}
	for _, seatID := range req.SeatIDs {
		err := h.seatService.LockSeat(ctx, req.EventID, seatID, authRecord.Id)
		if err == nil {
			lockedSeats = append(lockedSeats, seatID)
		}
	}

	return c.JSON(http.StatusOK, map[string]interface{}{
		"locked_seats": lockedSeats,
		"failed_count": len(req.SeatIDs) - len(lockedSeats),
	})
}

// UnlockSeatsBatch - Unlock multiple seats
func (h *SeatHandler) UnlockSeatsBatch(c echo.Context) error {
	authRecord, _ := c.Get(apis.ContextAuthRecordKey).(*models.Record)
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

	// Verify seats are locked by this user before unlocking
	unlockedSeats := []string{}
	for _, seatID := range req.SeatIDs {
		seatKey := fmt.Sprintf("seat:%s:%s", req.EventID, seatID)
		lockedBy, _ := h.seatService.Redis.HGet(ctx, seatKey, "locked_by").Result()

		if lockedBy == authRecord.Id {
			err := h.seatService.UnlockSeat(ctx, req.EventID, seatID)
			if err == nil {
				unlockedSeats = append(unlockedSeats, seatID)
			}
		}
	}

	return c.JSON(http.StatusOK, map[string]any{
		"unlocked_seats": unlockedSeats,
	})
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
