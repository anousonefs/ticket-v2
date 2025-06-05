// handlers/queue_handler.go
package handlers

import (
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"ticket-system/internal/services"

	"github.com/pocketbase/pocketbase"
	"github.com/pocketbase/pocketbase/apis"
	"github.com/pocketbase/pocketbase/core"
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
func (h *QueueHandler) EnterQueue(e *core.RequestEvent) error {
	println("start enter queue")
	// if e.Auth == nil {
	// 	return apis.NewUnauthorizedError("Unauthorized", nil)
	// }

	var req struct {
		EventID   string `json:"event_id"`
		SessionID string `json:"session_id"`
		UserID    string `json:"user_id"`
	}

	if err := e.BindBody(&req); err != nil {
		return apis.NewBadRequestError("Invalid request", err)
	}

	userID := req.UserID
	fmt.Printf("=> userID: %v\n", userID)

	if req.UserID == "" {
		slog.Info("user id is empty")
		return apis.NewBadRequestError("Invalid request", errors.New("user id must not empty"))
	}

	ctx := e.Request.Context()

	userKey := fmt.Sprintf("user:queue:%s:%s", req.EventID, userID)
	exists, _ := h.queueService.Redis.Exists(ctx, userKey).Result()
	if exists > 0 {
		return apis.NewBadRequestError("Already in queue", nil)
	}

	println("start service enq")
	if err := h.queueService.EnqueueUserAtomic(ctx, req.EventID, userID, req.SessionID); err != nil {
		return apis.NewBadRequestError("Failed to join queue", err)
	}

	return e.JSON(http.StatusOK, map[string]any{"message": "Successfully joined queue", "user_id": userID})
}

// GetQueuePosition - Get current queue position
func (h *QueueHandler) GetQueuePosition(e *core.RequestEvent) error {
	if e.Auth == nil {
		return apis.NewUnauthorizedError("Unauthorized", nil)
	}

	eventID := e.Request.URL.Query().Get("event_id")
	if eventID == "" {
		return apis.NewBadRequestError("Event ID required", nil)
	}
	ctx := e.Request.Context()

	posKey := fmt.Sprintf("queue:position:%s:%s", eventID, e.Auth.Id)
	position, err := h.queueService.Redis.Get(ctx, posKey).Int()
	if err != nil {
		slog.Warn("h.queueService.Redis.Get()", "posKey", err)
		position = 1
	}

	userKey := fmt.Sprintf("user:queue:%s:%s", eventID, e.Auth.Id)
	status, _ := h.queueService.Redis.HGet(ctx, userKey, "status").Result()

	metrics, _ := h.queueService.GetQueueMetrics(ctx, eventID)

	response := map[string]any{"position": position, "status": status}
	if metrics != nil {
		if total, ok := metrics["total_in_queue"].(string); ok {
			response["total_in_queue"] = total
		}
		if avg, ok := metrics["avg_wait_time"].(string); ok {
			response["avg_wait_time"] = avg
		}
	}

	return e.JSON(http.StatusOK, response)
}

// GetQueueMetrics - Get queue metrics for an event
func (h *QueueHandler) GetQueueMetrics(e *core.RequestEvent) error {
	eventID := e.Request.URL.Query().Get("event_id")
	if eventID == "" {
		return apis.NewBadRequestError("Event ID required", nil)
	}

	metrics, err := h.queueService.GetQueueMetrics(e.Request.Context(), eventID)
	if err != nil {
		return apis.NewBadRequestError("Failed to get metrics", err)
	}

	return e.JSON(http.StatusOK, metrics)
}

// LeaveQueue - Leave the queue
func (h *QueueHandler) LeaveQueue(e *core.RequestEvent) error {
	if e.Auth == nil {
		return apis.NewUnauthorizedError("Unauthorized", nil)
	}

	var req struct {
		EventID string `json:"event_id"`
	}
	if err := e.BindBody(&req); err != nil {
		return apis.NewBadRequestError("Invalid request", err)
	}
	ctx := e.Request.Context()

	userKey := fmt.Sprintf("user:queue:%s:%s", req.EventID, e.Auth.Id)
	status, _ := h.queueService.Redis.HGet(ctx, userKey, "status").Result()

	if status == "processing" {
		h.queueService.RemoveFromProcessing(ctx, req.EventID, e.Auth.Id)
	} else if status == "waiting" {
		h.queueService.Redis.Del(ctx, userKey)
	}

	return e.JSON(http.StatusOK, map[string]any{"message": "Successfully left queue"})
}

func (h *QueueHandler) LeaveQueue2(e *core.RequestEvent) error {
	var req struct {
		EventID string `json:"event_id"`
		UserID  string `json:"user_id"`
	}
	if err := e.BindBody(&req); err != nil {
		return apis.NewBadRequestError("Invalid request", err)
	}
	ctx := e.Request.Context()

	userKey := fmt.Sprintf("user:queue:%s:%s", req.EventID)
	status, _ := h.queueService.Redis.HGet(ctx, userKey, "status").Result()

	if status == "processing" {
		h.queueService.RemoveFromProcessing(ctx, req.EventID, req.UserID)
	} else if status == "waiting" {
		h.queueService.Redis.Del(ctx, userKey)
	}

	return e.JSON(http.StatusOK, map[string]any{"message": "Successfully left queue"})
}

func (h *QueueHandler) GetWaitingPage(e *core.RequestEvent) error {
	eventID := e.Request.PathValue("eventId")

	info, err := h.queueService.GetWaitingPageInfo(e.Request.Context(), eventID)
	if err != nil {
		slog.Error(fmt.Sprintf("h.queueService.GetWaitingPageInfo(%v)", eventID), "error", err)
		return e.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
	}

	return e.JSON(http.StatusOK, info)
}
