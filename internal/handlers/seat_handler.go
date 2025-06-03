package handlers

import (
	"fmt"
	"net/http"
	"ticket-system/internal/services"

	"github.com/pocketbase/dbx"
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
	fmt.Printf("=> event id: %v\n", eventID)

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
	fmt.Printf("=> seats: %v\n", seats)

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

// todo: add session id into seat key
func (h *SeatHandler) LockSeat(e *core.RequestEvent) error {
	if e.Auth == nil {
		return apis.NewUnauthorizedError("Unauthorized", nil)
	}

	var req struct {
		EventID string `json:"event_id"`
		SeatID  string `json:"seat_id"`
	}

	if err := e.BindBody(&req); err != nil {
		return apis.NewBadRequestError("Invalid request", err)
	}

	ctx := e.Request.Context()
	userID := e.Auth.Id

	// Call service to handle business logic
	err := h.seatService.LockSeatForUser(ctx, req.EventID, req.SeatID, userID)
	if err != nil {
		return apis.NewBadRequestError("Failed to lock seat: "+err.Error(), err)
	}

	return e.JSON(http.StatusOK, map[string]any{
		"message": "Seat locked successfully",
		"seat_id": req.SeatID,
	})
}

// UnlockSeatsBatch - Unlock multiple seats
func (h *SeatHandler) UnlockSeat(e *core.RequestEvent) error {
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

type SeatData struct {
	ID      string  `db:"id" json:"id"`
	Row     string  `db:"row" json:"row"`
	Number  int     `db:"number" json:"number"`
	Section string  `db:"section" json:"section"`
	Price   float64 `db:"price" json:"price"`
	EventID string  `db:"event_id" json:"event_id"`
	Status  string  `json:"status"` // Will be populated from Redis
}

func (h *SeatHandler) GetSeats2(e *core.RequestEvent) error {
	eventID := e.Request.PathValue("eventId")

	if eventID == "" {
		return apis.NewBadRequestError("Event ID is required", nil)
	}

	fmt.Printf("=> Getting seats for event: %v\n", eventID)

	// Direct SQL query to get seats with all needed data
	seats := []SeatData{}
	err := h.app.DB().
		Select(
			"id",
			"row",
			"number",
			"section",
			"price",
			"status",
			"event_id",
		).
		From("seats").
		Where(dbx.HashExp{"event_id": eventID}).
		OrderBy("section ASC", "row ASC", "number ASC").
		All(&seats)
	if err != nil {
		fmt.Printf("=> Database error: %v\n", err)
		return apis.NewBadRequestError("Failed to fetch seats", err)
	}

	fmt.Printf("=> Found %d seats\n", len(seats))

	if len(seats) == 0 {
		return e.JSON(http.StatusOK, map[string]any{
			"sections":        map[string][]SeatData{},
			"total_seats":     0,
			"available_seats": 0,
		})
	}

	// Extract seat IDs for availability check
	seatIDs := make([]string, len(seats))
	for i, seat := range seats {
		seatIDs[i] = seat.ID
	}

	// Get seat availability from Redis
	availability, err := h.seatService.GetSeatAvailability(e.Request.Context(), eventID, seatIDs)
	if err != nil {
		fmt.Printf("=> Redis availability error: %v\n", err)
		return apis.NewBadRequestError("Failed to get seat availability", err)
	}

	// Group seats by section and add status
	sections := make(map[string][]SeatData)
	availableCount := 0

	for _, seat := range seats {
		// Add status from Redis availability
		status, exists := availability[seat.ID]
		if !exists {
			status = "available" // Default status if not found in Redis
		}
		seat.Status = status

		// Count available seats
		if status == "available" {
			availableCount++
		}

		// Group by section
		sections[seat.Section] = append(sections[seat.Section], seat)
	}

	fmt.Printf("=> Processed %d seats across %d sections, %d available\n",
		len(seats), len(sections), availableCount)

	return e.JSON(http.StatusOK, map[string]any{
		"seats":           seats,
		"sections":        sections,
		"total_seats":     len(seats),
		"available_seats": availableCount,
		"event_id":        eventID,
	})
}

// ALTERNATIVE VERSION - With more detailed response structure

func (h *SeatHandler) GetSeatsDetailed(e *core.RequestEvent) error {
	eventID := e.Request.PathValue("eventId")

	if eventID == "" {
		return apis.NewBadRequestError("Event ID is required", nil)
	}

	// Query with more detailed seat information
	query := `
		SELECT 
			s.id,
			s.row,
			s.number,
			s.section,
			s.price,
			s.event_id,
			s.created,
			s.updated,
			e.name as event_name
		FROM seats s
		LEFT JOIN events e ON s.event_id = e.id
		WHERE s.event_id = {:eventId}
		ORDER BY s.section ASC, s.row ASC, s.number ASC
	`

	type DetailedSeatData struct {
		ID        string  `db:"id" json:"id"`
		Row       string  `db:"row" json:"row"`
		Number    int     `db:"number" json:"number"`
		Section   string  `db:"section" json:"section"`
		Price     float64 `db:"price" json:"price"`
		EventID   string  `db:"event_id" json:"event_id"`
		Created   string  `db:"created" json:"created"`
		Updated   string  `db:"updated" json:"updated"`
		EventName string  `db:"event_name" json:"event_name"`
		Status    string  `json:"status"`
	}

	seats := []DetailedSeatData{}
	err := h.app.DB().NewQuery(query).Bind(map[string]any{
		"eventId": eventID,
	}).All(&seats)
	if err != nil {
		return apis.NewBadRequestError("Failed to fetch seats", err)
	}

	if len(seats) == 0 {
		return e.JSON(http.StatusOK, map[string]any{
			"message":  "No seats found for this event",
			"event_id": eventID,
		})
	}

	// Get availability
	seatIDs := make([]string, len(seats))
	for i, seat := range seats {
		seatIDs[i] = seat.ID
	}

	availability, err := h.seatService.GetSeatAvailability(e.Request.Context(), eventID, seatIDs)
	if err != nil {
		return apis.NewBadRequestError("Failed to get seat availability", err)
	}

	// Process seats with statistics
	sections := make(map[string][]DetailedSeatData)
	sectionStats := make(map[string]map[string]int)

	for _, seat := range seats {
		seat.Status = availability[seat.ID]
		if seat.Status == "" {
			seat.Status = "available"
		}

		// Group by section
		sections[seat.Section] = append(sections[seat.Section], seat)

		// Calculate section statistics
		if sectionStats[seat.Section] == nil {
			sectionStats[seat.Section] = map[string]int{
				"total":     0,
				"available": 0,
				"locked":    0,
				"sold":      0,
			}
		}

		sectionStats[seat.Section]["total"]++
		sectionStats[seat.Section][seat.Status]++
	}

	return e.JSON(http.StatusOK, map[string]any{
		"sections":        sections,
		"section_stats":   sectionStats,
		"total_seats":     len(seats),
		"available_seats": countAvailableInMap(availability),
		"event_id":        eventID,
		"event_name":      seats[0].EventName,
	})
}

// OPTIMIZED VERSION - Single query with Redis data if possible

func (h *SeatHandler) GetSeatsOptimized(e *core.RequestEvent) error {
	eventID := e.Request.PathValue("eventId")

	if eventID == "" {
		return apis.NewBadRequestError("Event ID is required", nil)
	}

	// Use raw SQL for better performance
	query := `
		SELECT 
			id,
			row,
			number,
			section,
			price,
			event_id
		FROM seats 
		WHERE event_id = ?
		ORDER BY 
			CASE section 
				WHEN 'VIP' THEN 1 
				WHEN 'Premium' THEN 2 
				WHEN 'Standard' THEN 3 
				ELSE 4 
			END,
			row ASC, 
			number ASC
	`

	seats := []SeatData{}
	err := h.app.DB().NewQuery(query).All(&seats)
	if err != nil {
		return apis.NewBadRequestError("Failed to fetch seats", err)
	}

	if len(seats) == 0 {
		return e.JSON(http.StatusNotFound, map[string]any{
			"error":    "No seats found for this event",
			"event_id": eventID,
		})
	}

	// Batch get availability from Redis
	seatIDs := make([]string, len(seats))
	for i, seat := range seats {
		seatIDs[i] = seat.ID
	}

	availability, err := h.seatService.GetSeatAvailability(e.Request.Context(), eventID, seatIDs)
	if err != nil {
		// If Redis fails, mark all as available (graceful degradation)
		fmt.Printf("Redis availability check failed, defaulting to available: %v\n", err)
		availability = make(map[string]string)
		for _, seatID := range seatIDs {
			availability[seatID] = "available"
		}
	}

	// Build response efficiently
	sections := make(map[string][]map[string]any)
	totalSeats := len(seats)
	availableSeats := 0

	for _, seat := range seats {
		status := availability[seat.ID]
		if status == "" {
			status = "available"
		}

		if status == "available" {
			availableSeats++
		}

		seatData := map[string]any{
			"id":      seat.ID,
			"row":     seat.Row,
			"number":  seat.Number,
			"section": seat.Section,
			"price":   seat.Price,
			"status":  status,
		}

		sections[seat.Section] = append(sections[seat.Section], seatData)
	}

	return e.JSON(http.StatusOK, map[string]any{
		"sections":        sections,
		"total_seats":     totalSeats,
		"available_seats": availableSeats,
		"event_id":        eventID,
	})
}

// Helper function to count available seats
func countAvailableInMap(availability map[string]string) int {
	count := 0
	for _, status := range availability {
		if status == "available" {
			count++
		}
	}
	return count
}

// BENCHMARK VERSION - For performance testing

func (h *SeatHandler) GetSeatsBenchmark(e *core.RequestEvent) error {
	eventID := e.Request.PathValue("eventId")

	// Single optimized query with minimal data transfer
	seats := []struct {
		ID      string  `db:"id"`
		Row     string  `db:"row"`
		Number  int     `db:"number"`
		Section string  `db:"section"`
		Price   float64 `db:"price"`
	}{}

	err := h.app.DB().
		Select("id", "row", "number", "section", "price").
		From("seats").
		Where(dbx.HashExp{"event_id": eventID}).
		OrderBy("section", "row", "number").
		All(&seats)
	if err != nil {
		return apis.NewBadRequestError("Database query failed", err)
	}

	// Quick response without Redis if no seats
	if len(seats) == 0 {
		return e.JSON(http.StatusOK, map[string]any{
			"sections":        map[string][]any{},
			"total_seats":     0,
			"available_seats": 0,
		})
	}

	// Parallel Redis availability check
	seatIDs := make([]string, len(seats))
	for i, seat := range seats {
		seatIDs[i] = seat.ID
	}

	availability, _ := h.seatService.GetSeatAvailability(e.Request.Context(), eventID, seatIDs)

	// Fast response building
	result := map[string]any{
		"sections":        make(map[string][]map[string]any),
		"total_seats":     len(seats),
		"available_seats": 0,
	}

	sections := result["sections"].(map[string][]map[string]any)
	availableCount := 0

	for _, seat := range seats {
		status := availability[seat.ID]
		if status == "" {
			status = "available"
		}
		if status == "available" {
			availableCount++
		}

		sections[seat.Section] = append(sections[seat.Section], map[string]any{
			"id":     seat.ID,
			"row":    seat.Row,
			"number": seat.Number,
			"price":  seat.Price,
			"status": status,
		})
	}

	result["available_seats"] = availableCount
	return e.JSON(http.StatusOK, result)
}
