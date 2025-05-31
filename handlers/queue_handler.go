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

func (h *QueueHandler) EnterQueue(c echo.Context) error {
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

	err := h.queueService.EnqueueUser(c.Request().Context(), req.EventID, authRecord.Id, req.SessionID)
	if err != nil {
		return apis.NewBadRequestError("Failed to join queue", err)
	}

	return c.JSON(http.StatusOK, map[string]any{
		"message": "Successfully joined queue",
		"user_id": authRecord.Id,
	})
}

func (h *QueueHandler) GetQueuePosition(c echo.Context) error {
	authRecord, _ := c.Get(apis.ContextAuthRecordKey).(*models.Record)
	if authRecord == nil {
		return apis.NewUnauthorizedError("Unauthorized", nil)
	}

	eventID := c.QueryParam("event_id")

	posKey := fmt.Sprintf("queue:position:%s:%s", eventID, authRecord.Id)
	position, err := h.queueService.Redis.Get(c.Request().Context(), posKey).Int()
	if err != nil {
		position = -1
	}

	userKey := fmt.Sprintf("user:queue:%s:%s", eventID, authRecord.Id)
	status, _ := h.queueService.Redis.HGet(c.Request().Context(), userKey, "status").Result()

	return c.JSON(http.StatusOK, map[string]any{
		"position": position,
		"status":   status,
	})
}
