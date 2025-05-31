package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strconv"
	"ticket-system/models"
	"ticket-system/services"
	"time"

	"github.com/labstack/echo/v5"
	"github.com/pocketbase/pocketbase"
	"github.com/pocketbase/pocketbase/apis"
	pbmodels "github.com/pocketbase/pocketbase/models"
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
func (h *AdminHandler) GetQueueDashboard(c echo.Context) error {
	// Check admin permission
	authRecord, _ := c.Get(apis.ContextAuthRecordKey).(*pbmodels.Record)
	if authRecord == nil || authRecord.Collection().Name != "admins" {
		return apis.NewUnauthorizedError("Admin access required", nil)
	}

	ctx := c.Request().Context()

	// Get all active events
	eventIDs, err := h.redis.SMembers(ctx, "active_events").Result()
	if err != nil {
		return apis.NewBadRequestError("Failed to get active events", err)
	}

	dashboardData := []map[string]interface{}{}

	for _, eventID := range eventIDs {
		// Get event details from database
		event, err := h.app.Dao().FindRecordById("events", eventID)
		if err != nil {
			continue
		}

		// Get queue metrics
		metrics, _ := h.queueService.GetQueueMetrics(ctx, eventID)

		// Get total processed count
		processedKey := fmt.Sprintf("queue:processed:%s", eventID)
		totalProcessed, _ := h.redis.Get(ctx, processedKey).Int()

		eventData := map[string]interface{}{
			"event_id":         eventID,
			"event_name":       event.GetString("name"),
			"total_in_queue":   0,
			"processing_count": 0,
			"avg_wait_time":    0,
			"total_processed":  totalProcessed,
		}

		// Parse metrics
		if metrics != nil {
			if val, ok := metrics["total_in_queue"].(string); ok {
				eventData["total_in_queue"], _ = strconv.Atoi(val)
			}
			if val, ok := metrics["processing_count"].(int64); ok {
				eventData["processing_count"] = val
			}
			if val, ok := metrics["avg_wait_time"].(string); ok {
				if avgWait, err := strconv.ParseFloat(val, 64); err == nil {
					eventData["avg_wait_time"] = avgWait
				}
			}
		}

		dashboardData = append(dashboardData, eventData)
	}

	return c.JSON(http.StatusOK, dashboardData)
}

// GetQueueDetails - Get detailed queue information for an event
func (h *AdminHandler) GetQueueDetails(c echo.Context) error {
	// Check admin permission
	authRecord, _ := c.Get(apis.ContextAuthRecordKey).(*pbmodels.Record)
	if authRecord == nil || authRecord.Collection().Name != "admins" {
		return apis.NewUnauthorizedError("Admin access required", nil)
	}

	eventID := c.QueryParam("event_id")
	if eventID == "" {
		return apis.NewBadRequestError("Event ID required", nil)
	}

	ctx := c.Request().Context()
	queueKey := fmt.Sprintf("queue:waiting:%s", eventID)

	// Get all queue entries
	entries, err := h.redis.LRange(ctx, queueKey, 0, -1).Result()
	if err != nil {
		return apis.NewBadRequestError("Failed to get queue details", err)
	}

	details := []map[string]interface{}{}

	for i, entryData := range entries {
		var entry models.QueueEntry
		if err := json.Unmarshal([]byte(entryData), &entry); err != nil {
			continue
		}

		// Get user details
		user, _ := h.app.Dao().FindRecordById("users", entry.UserID)

		// if user != nil else ""

		details = append(details, map[string]any{
			"position":   i + 1,
			"user_id":    entry.UserID,
			"user_email": user.GetString("email"),
			"joined_at":  entry.JoinedAt,
			"wait_time":  time.Since(entry.JoinedAt).Seconds(),
			"session_id": entry.SessionID,
		})
	}

	return c.JSON(http.StatusOK, details)
}

// ForceProcessQueue - Manually trigger queue processing
func (h *AdminHandler) ForceProcessQueue(c echo.Context) error {
	// Check admin permission
	authRecord, _ := c.Get(apis.ContextAuthRecordKey).(*pbmodels.Record)
	if authRecord == nil || authRecord.Collection().Name != "admins" {
		return apis.NewUnauthorizedError("Admin access required", nil)
	}

	var req struct {
		EventID string `json:"event_id"`
	}

	if err := c.Bind(&req); err != nil {
		return apis.NewBadRequestError("Invalid request", err)
	}

	// Trigger queue processing
	go h.queueService.ProcessQueue(context.Background(), req.EventID)

	return c.JSON(http.StatusOK, map[string]interface{}{
		"message": "Queue processing triggered",
	})
}

// RemoveFromQueue - Remove a user from queue (admin action)
func (h *AdminHandler) RemoveFromQueue(c echo.Context) error {
	// Check admin permission
	authRecord, _ := c.Get(apis.ContextAuthRecordKey).(*pbmodels.Record)
	if authRecord == nil || authRecord.Collection().Name != "admins" {
		return apis.NewUnauthorizedError("Admin access required", nil)
	}

	var req struct {
		EventID string `json:"event_id"`
		UserID  string `json:"user_id"`
		Reason  string `json:"reason"`
	}

	if err := c.Bind(&req); err != nil {
		return apis.NewBadRequestError("Invalid request", err)
	}

	ctx := c.Request().Context()

	// Log admin action
	log.Printf("Admin %s removing user %s from event %s queue. Reason: %s",
		authRecord.Id, req.UserID, req.EventID, req.Reason)

	// Get user status
	userKey := fmt.Sprintf("user:queue:%s:%s", req.EventID, req.UserID)
	status, _ := h.redis.HGet(ctx, userKey, "status").Result()

	if status == "processing" {
		h.queueService.RemoveFromProcessing(ctx, req.EventID, req.UserID)
		go h.queueService.ProcessQueue(ctx, req.EventID)
	} else if status == "waiting" {
		// Complex operation to remove from middle of queue
		// For now, just mark as removed
		h.redis.Del(ctx, userKey)
	}

	// Notify user they were removed
	channel := fmt.Sprintf("user-%s", req.UserID)
	h.queueService.PubNub.Publish().
		Channel(channel).
		Message(map[string]interface{}{
			"type":     "queue_removed",
			"reason":   req.Reason,
			"event_id": req.EventID,
		}).
		Execute()

	return c.JSON(http.StatusOK, map[string]interface{}{
		"message": "User removed from queue",
	})
}
