// main.go
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"
	"ticket-system/config"
	"ticket-system/handlers"
	"ticket-system/models"
	"ticket-system/services"
	"ticket-system/utils"
	"time"

	"github.com/labstack/echo/v5"
	"github.com/pocketbase/pocketbase"
	"github.com/pocketbase/pocketbase/core"
	"github.com/pocketbase/pocketbase/plugins/migratecmd"
	pubnub "github.com/pubnub/go"
	"github.com/redis/go-redis/v9"
)

func main() {
	app := pocketbase.New()

	// Load configuration
	cfg := config.LoadConfig()

	// Initialize Redis
	redisClient := utils.NewRedisClient(cfg.RedisURL)
	defer redisClient.Close()

	// Initialize PubNub
	pnConfig := pubnub.NewConfig()
	pnConfig.PublishKey = cfg.PubNubPublishKey
	pnConfig.SubscribeKey = cfg.PubNubSubscribeKey
	pnConfig.SecretKey = cfg.PubNubSecretKey

	pn := pubnub.NewPubNub(pnConfig)

	// Initialize services
	queueService := services.NewQueueService(redisClient, pn, cfg)
	seatService := services.NewSeatService(redisClient)
	paymentService := services.NewPaymentService(redisClient, pn, queueService)

	// Initialize handlers
	queueHandler := handlers.NewQueueHandler(app, queueService)
	seatHandler := handlers.NewSeatHandler(app, seatService)
	bookingHandler := handlers.NewBookingHandler(app, seatService, paymentService)
	paymentHandler := handlers.NewPaymentHandler(app, paymentService)
	adminHandler := handlers.NewAdminHandler(app, queueService, redisClient)

	// Enable migrations
	migratecmd.MustRegister(app, app.RootCmd, migratecmd.Config{
		Automigrate: true,
	})

	// Create context for background tasks
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Start background tasks
	go queueService.UpdateQueuePositions(ctx)
	go queueService.CleanupInactiveQueues(ctx)
	go restoreQueueState(redisClient, queueService)

	// Setup graceful shutdown
	go handleShutdown(cancel)

	// Register routes
	app.OnBeforeServe().Add(func(e *core.ServeEvent) error {
		// Queue endpoints
		e.Router.POST("/api/queue/enter", queueHandler.EnterQueue)
		e.Router.GET("/api/queue/position", queueHandler.GetQueuePosition)
		e.Router.GET("/api/queue/metrics", queueHandler.GetQueueMetrics)
		e.Router.POST("/api/queue/leave", queueHandler.LeaveQueue)

		// Seat endpoints
		e.Router.GET("/api/events/:eventId/seats", seatHandler.GetSeats)
		e.Router.POST("/api/seats/lock-batch", seatHandler.LockSeatsBatch)
		e.Router.POST("/api/seats/unlock-batch", seatHandler.UnlockSeatsBatch)

		// Booking endpoints
		e.Router.POST("/api/booking/confirm", bookingHandler.ConfirmBooking)
		e.Router.GET("/api/booking/history", bookingHandler.GetBookingHistory)

		// Payment endpoints
		e.Router.GET("/api/payment/:paymentId", paymentHandler.GetPaymentDetails)
		e.Router.GET("/api/payment/:paymentId/status", paymentHandler.CheckPaymentStatus)
		e.Router.POST("/api/payment/:paymentId/cancel", paymentHandler.CancelPayment)

		// Admin endpoints
		e.Router.GET("/api/admin/queue-dashboard", adminHandler.GetQueueDashboard)
		e.Router.GET("/api/admin/queue-details", adminHandler.GetQueueDetails)
		e.Router.POST("/api/admin/force-process-queue", adminHandler.ForceProcessQueue)
		e.Router.POST("/api/admin/remove-from-queue", adminHandler.RemoveFromQueue)

		// Test endpoint for payment simulation
		if cfg.Environment == "development" {
			e.Router.POST("/api/test/simulate-payment", paymentHandler.SimulatePayment)
		}

		// Health check
		e.Router.GET("/health", func(c echo.Context) error {
			// Check Redis connection
			if err := utils.RedisHealthCheck(redisClient); err != nil {
				return c.JSON(503, map[string]string{
					"status": "unhealthy",
					"error":  err.Error(),
				})
			}

			return c.JSON(200, map[string]string{
				"status": "healthy",
			})
		})

		log.Println("Server routes registered")
		return nil
	})

	// Start server
	if err := app.Start(); err != nil {
		log.Fatal(err)
	}
}

// restoreQueueState restores queue state from Redis on server restart
func restoreQueueState(redisClient *redis.Client, queueService *services.QueueService) {
	ctx := context.Background()

	log.Println("Restoring queue state from Redis...")

	// Get all active events
	eventIDs, err := redisClient.SMembers(ctx, "active_events").Result()
	if err != nil {
		log.Printf("Error getting active events: %v", err)
		return
	}

	log.Printf("Found %d active events", len(eventIDs))

	// Process each event's queue
	for _, eventID := range eventIDs {
		// Check waiting queue
		waitingKey := fmt.Sprintf("queue:waiting:%s", eventID)
		queueLen, _ := redisClient.LLen(ctx, waitingKey).Result()

		if queueLen > 0 {
			log.Printf("Event %s has %d users in waiting queue", eventID, queueLen)
			// Trigger queue processing
			go queueService.ProcessQueue(ctx, eventID)
		}

		// Check processing queue
		processingKey := fmt.Sprintf("queue:processing:%s", eventID)
		processingCount, _ := redisClient.SCard(ctx, processingKey).Result()

		if processingCount > 0 {
			log.Printf("Event %s has %d users in processing", eventID, processingCount)
			// Re-establish timeouts for processing users
			members, _ := redisClient.SMembers(ctx, processingKey).Result()
			for _, member := range members {
				var user models.ProcessingUser
				if err := json.Unmarshal([]byte(member), &user); err == nil {
					// Calculate remaining time and set timeout
					elapsed := time.Since(user.StartedAt)
					remaining := queueService.Config.ProcessingTimeout - elapsed
					if remaining > 0 {
						go func(eventID, userID string, timeout time.Duration) {
							time.Sleep(timeout)
							queueService.RemoveFromProcessing(ctx, eventID, userID)
							queueService.ProcessQueue(ctx, eventID)
						}(eventID, user.UserID, remaining)
					} else {
						// Timeout already passed, remove from processing
						go func(eventID, userID string) {
							queueService.RemoveFromProcessing(ctx, eventID, userID)
							queueService.ProcessQueue(ctx, eventID)
						}(eventID, user.UserID)
					}
				}
			}
		}
	}

	log.Println("Queue state restoration completed")
}

// handleShutdown handles graceful shutdown
func handleShutdown(cancel context.CancelFunc) {
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)

	<-sigChan
	log.Println("Shutdown signal received, cleaning up...")
	cancel()
}
