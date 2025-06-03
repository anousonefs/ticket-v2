package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strconv"
	"ticket-system/internal/services"
	"ticket-system/models"
	"time"

	"github.com/pocketbase/pocketbase"
	"github.com/pocketbase/pocketbase/apis"
	"github.com/pocketbase/pocketbase/core"
	"github.com/redis/go-redis/v9"
)

type AdminHandler struct {
	app          *pocketbase.PocketBase
	queueService *services.QueueService
	redis        *redis.Client
}

func NewAdminHandler(app *pocketbase.PocketBase, queueService *services.QueueService, redis *redis.Client) *AdminHandler {
	return &AdminHandler{
		app:          app,
		queueService: queueService,
		redis:        redis,
	}
}

// GetQueueDashboard - Get dashboard data for all events
func (h *AdminHandler) GetQueueDashboard(e *core.RequestEvent) error {
	if e.Auth == nil || e.Auth.Collection().Name != "admins" {
		return apis.NewUnauthorizedError("Admin access required", nil)
	}
	ctx := e.Request.Context()

	eventIDs, err := h.redis.SMembers(ctx, "active_events").Result()
	if err != nil {
		return apis.NewBadRequestError("Failed to get active events", err)
	}

	dashboardData := []map[string]any{}
	for _, eventID := range eventIDs {
		event, err := h.app.FindRecordById("events", eventID)
		if err != nil {
			continue
		}
		metrics, _ := h.queueService.GetQueueMetrics(ctx, eventID)
		processedKey := fmt.Sprintf("queue:processed:%s", eventID)
		totalProcessed, _ := h.redis.Get(ctx, processedKey).Int()

		eventData := map[string]any{
			"event_id":         eventID,
			"event_name":       event.GetString("name"),
			"total_in_queue":   0,
			"processing_count": 0,
			"avg_wait_time":    0,
			"total_processed":  totalProcessed,
		}
		if metrics != nil {
			if val, ok := metrics["total_in_queue"].(string); ok {
				if v, err := strconv.Atoi(val); err == nil {
					eventData["total_in_queue"] = v
				}
			}
			if val, ok := metrics["processing_count"].(int64); ok {
				eventData["processing_count"] = val
			}
			if val, ok := metrics["avg_wait_time"].(string); ok {
				if avg, err := strconv.ParseFloat(val, 64); err == nil {
					eventData["avg_wait_time"] = avg
				}
			}
		}
		dashboardData = append(dashboardData, eventData)
	}
	return e.JSON(http.StatusOK, dashboardData)
}

// GetQueueDetails - Get detailed queue information for an event
func (h *AdminHandler) GetQueueDetails(e *core.RequestEvent) error {
	if e.Auth == nil || e.Auth.Collection().Name != "admins" {
		return apis.NewUnauthorizedError("Admin access required", nil)
	}

	eventID := e.Request.URL.Query().Get("event_id")
	if eventID == "" {
		return apis.NewBadRequestError("Event ID required", nil)
	}
	ctx := e.Request.Context()
	queueKey := fmt.Sprintf("queue:waiting:%s", eventID)

	entries, err := h.redis.LRange(ctx, queueKey, 0, -1).Result()
	if err != nil {
		return apis.NewBadRequestError("Failed to get queue details", err)
	}

	details := []map[string]any{}
	for i, entryData := range entries {
		var entry models.QueueEntry
		if err := json.Unmarshal([]byte(entryData), &entry); err != nil {
			continue
		}
		user, _ := h.app.FindRecordById("users", entry.UserID)
		details = append(details, map[string]any{
			"position":   i + 1,
			"user_id":    entry.UserID,
			"user_email": user.GetString("email"),
			"joined_at":  entry.JoinedAt,
			"wait_time":  time.Since(entry.JoinedAt).Seconds(),
			"session_id": entry.SessionID,
		})
	}
	return e.JSON(http.StatusOK, details)
}

// ForceProcessQueue - Manually trigger queue processing
func (h *AdminHandler) ForceProcessQueue(e *core.RequestEvent) error {
	if e.Auth == nil || e.Auth.Collection().Name != "admins" {
		return apis.NewUnauthorizedError("Admin access required", nil)
	}

	var req struct {
		EventID string `json:"event_id"`
	}
	if err := e.BindBody(&req); err != nil {
		return apis.NewBadRequestError("Invalid request", err)
	}
	go h.queueService.ProcessQueue(context.Background(), req.EventID)
	return e.JSON(http.StatusOK, map[string]any{"message": "Queue processing triggered"})
}

// RemoveFromQueue - Remove a user from queue (admin action)
func (h *AdminHandler) RemoveFromQueue(e *core.RequestEvent) error {
	if e.Auth == nil || e.Auth.Collection().Name != "admins" {
		return apis.NewUnauthorizedError("Admin access required", nil)
	}

	var req struct {
		EventID string `json:"event_id"`
		UserID  string `json:"user_id"`
		Reason  string `json:"reason"`
	}
	if err := e.BindBody(&req); err != nil {
		return apis.NewBadRequestError("Invalid request", err)
	}
	ctx := e.Request.Context()

	log.Printf("Admin %s removing user %s from event %s queue. Reason: %s",
		e.Auth.Id, req.UserID, req.EventID, req.Reason)

	userKey := fmt.Sprintf("user:queue:%s:%s", req.EventID, req.UserID)
	status, _ := h.redis.HGet(ctx, userKey, "status").Result()
	if status == "processing" {
		h.queueService.RemoveFromProcessing(ctx, req.EventID, req.UserID)
		go h.queueService.ProcessQueue(ctx, req.EventID)
	} else if status == "waiting" {
		h.redis.Del(ctx, userKey)
	}

	channel := fmt.Sprintf("user-%s", req.UserID)
	h.queueService.PubNub.Publish().Channel(channel).
		Message(map[string]any{"type": "queue_removed", "reason": req.Reason, "event_id": req.EventID}).
		Execute()

	return e.JSON(http.StatusOK, map[string]any{"message": "User removed from queue"})
}
