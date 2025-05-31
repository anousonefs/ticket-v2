// handlers/queue_handler.go
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

type QueueHandler struct {
	app          *pocketbase.PocketBase
	queueService *services.QueueService
}

func NewQueueHandler(app *pocketbase.PocketBase, queueService *services.QueueService) *QueueHandler {
	return &QueueHandler{
		app:          app,
		queueService: queueService,
	}
}

// EnterQueue - Enter the queue endpoint
func (h *QueueHandler) EnterQueue(c echo.Context) error {
	// Get authenticated user
	authRecord, _ := c.Get(apis.ContextAuthRecordKey).(*models.Record)
	if authRecord == nil {
		return apis.NewUnauthorizedError("Unauthorized", nil)
	}

	var req struct {
		EventID   string `json:"event_id"`
		SessionID string `json:"session_id"`
	}

	if err := c.Bind(&req); err != nil {
		return apis.NewBadRequestError("Invalid request", err)
	}

	// Check if user is already in queue
	userKey := fmt.Sprintf("user:queue:%s:%s", req.EventID, authRecord.Id)
	exists, _ := h.queueService.Redis.Exists(c.Request().Context(), userKey).Result()
	if exists > 0 {
		return apis.NewBadRequestError("Already in queue", nil)
	}

	// Add user to queue
	err := h.queueService.EnqueueUser(c.Request().Context(), req.EventID, authRecord.Id, req.SessionID)
	if err != nil {
		return apis.NewBadRequestError("Failed to join queue", err)
	}

	return c.JSON(http.StatusOK, map[string]any{
		"message": "Successfully joined queue",
		"user_id": authRecord.Id,
	})
}

// GetQueuePosition - Get current queue position
func (h *QueueHandler) GetQueuePosition(c echo.Context) error {
	authRecord, _ := c.Get(apis.ContextAuthRecordKey).(*models.Record)
	if authRecord == nil {
		return apis.NewUnauthorizedError("Unauthorized", nil)
	}

	eventID := c.QueryParam("event_id")
	if eventID == "" {
		return apis.NewBadRequestError("Event ID required", nil)
	}

	ctx := c.Request().Context()

	// Get position from Redis
	posKey := fmt.Sprintf("queue:position:%s:%s", eventID, authRecord.Id)
	position, err := h.queueService.Redis.Get(ctx, posKey).Int()
	if err != nil {
		position = -1
	}

	// Get user status
	userKey := fmt.Sprintf("user:queue:%s:%s", eventID, authRecord.Id)
	status, _ := h.queueService.Redis.HGet(ctx, userKey, "status").Result()

	// Get queue metrics for additional info
	metrics, _ := h.queueService.GetQueueMetrics(ctx, eventID)

	response := map[string]any{
		"position": position,
		"status":   status,
	}

	if metrics != nil {
		if totalInQueue, ok := metrics["total_in_queue"].(string); ok {
			response["total_in_queue"] = totalInQueue
		}
		if avgWaitTime, ok := metrics["avg_wait_time"].(string); ok {
			response["avg_wait_time"] = avgWaitTime
		}
	}

	return c.JSON(http.StatusOK, response)
}

// GetQueueMetrics - Get queue metrics for an event
func (h *QueueHandler) GetQueueMetrics(c echo.Context) error {
	eventID := c.QueryParam("event_id")
	if eventID == "" {
		return apis.NewBadRequestError("Event ID required", nil)
	}

	metrics, err := h.queueService.GetQueueMetrics(c.Request().Context(), eventID)
	if err != nil {
		return apis.NewBadRequestError("Failed to get metrics", err)
	}

	return c.JSON(http.StatusOK, metrics)
}

// LeaveQueue - Leave the queue
func (h *QueueHandler) LeaveQueue(c echo.Context) error {
	authRecord, _ := c.Get(apis.ContextAuthRecordKey).(*models.Record)
	if authRecord == nil {
		return apis.NewUnauthorizedError("Unauthorized", nil)
	}

	var req struct {
		EventID string `json:"event_id"`
	}

	if err := c.Bind(&req); err != nil {
		return apis.NewBadRequestError("Invalid request", err)
	}

	ctx := c.Request().Context()

	// Remove from queue based on status
	userKey := fmt.Sprintf("user:queue:%s:%s", req.EventID, authRecord.Id)
	status, _ := h.queueService.Redis.HGet(ctx, userKey, "status").Result()

	if status == "processing" {
		h.queueService.RemoveFromProcessing(ctx, req.EventID, authRecord.Id)
	} else if status == "waiting" {
		// Remove from waiting queue - need to rebuild queue without this user
		// This is a complex operation, might want to implement a different approach
		// For now, just mark as left
		h.queueService.Redis.Del(ctx, userKey)
	}

	return c.JSON(http.StatusOK, map[string]any{
		"message": "Successfully left queue",
	})
}
