package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"ticket-system/config"
	"ticket-system/internal/handlers"
	"ticket-system/internal/services"
	"ticket-system/internal/services/bank/jdb"
	"ticket-system/models"
	"ticket-system/utils"
	"time"

	"github.com/pocketbase/dbx"

	"github.com/pocketbase/pocketbase/core"
	"github.com/pocketbase/pocketbase/plugins/migratecmd"
	pubnub "github.com/pubnub/go"
	"github.com/redis/go-redis/v9"

	"github.com/pocketbase/pocketbase"
)

func Start() error {
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

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	jdbInstance, err := jdb.New(ctx, &cfg.JDBConfig)
	if err != nil {
		return err
	}

	if jdbInstance != nil {
		go func() {
			txChannel := make(chan *jdb.Transaction, 1)
			jdbInstance.SetTranChannel(txChannel)
			for {
				select {
				case t := <-txChannel:
					slog.Info("=> yespay retrieve transaction", "txChannel", t)

					// if err := tranService.YesPay(ctx, t.UUID); err != nil {
					// 	slog.Error("transService.YesPay()", "error", err)
					// }
				}
			}
		}()
	}

	// Initialize services
	queueService := services.NewQueueService(redisClient, pn, cfg)
	seatService := services.NewSeatService(redisClient)
	paymentService := services.NewPaymentService(redisClient, pn, queueService, jdbInstance)

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

	// Start background tasks
	go queueService.UpdateQueuePositions(ctx)
	go queueService.CleanupInactiveQueues(ctx)

	// Setup graceful shutdown
	go handleShutdown(cancel)

	app.OnServe().BindFunc(func(e *core.ServeEvent) error {
		syncActiveEventsToRedis(app, redisClient)
		go restoreQueueState(redisClient, queueService)

		// Queue endpoints
		e.Router.POST("/api/v1/queue/enter", queueHandler.EnterQueue)
		e.Router.GET("/api/v1/queue/position", queueHandler.GetQueuePosition)
		e.Router.GET("/api/v1/queue/metrics", queueHandler.GetQueueMetrics)
		e.Router.POST("/api/v1/queue/leave", queueHandler.LeaveQueue)
		e.Router.GET("/api/v1/events/{eventId}/waiting", queueHandler.GetWaitingPage)

		// Seat endpoints
		e.Router.GET("/api/v1/events/{eventId}/seats", seatHandler.GetSeats2)
		e.Router.POST("/api/v1/seats/lock", seatHandler.LockSeat)
		e.Router.POST("/api/v1/seats/unlock-batch", seatHandler.UnlockSeat)

		// Booking endpoints
		e.Router.POST("/api/v1/booking/confirm", bookingHandler.ConfirmBooking)
		e.Router.GET("/api/v1/booking/history", bookingHandler.GetBookingHistory)

		// jdb
		e.Router.POST("/api/v1/payment/gen-jdb-qr", paymentHandler.GenJdbQr)

		// Payment endpoints
		e.Router.GET("/api/v1/payment/{paymentId}", paymentHandler.GetPaymentDetails)
		e.Router.GET("/api/v1/payment/{paymentId}/status", paymentHandler.CheckPaymentStatus)
		e.Router.POST("/api/v1/payment/{paymentId}/cancel", paymentHandler.CancelPayment)

		// Admin endpoints
		e.Router.GET("/api/v1/admin/queue-dashboard", adminHandler.GetQueueDashboard)
		e.Router.GET("/api/v1/admin/queue-details", adminHandler.GetQueueDetails)
		e.Router.POST("/api/v1/admin/force-process-queue", adminHandler.ForceProcessQueue)
		e.Router.POST("/api/v1/admin/remove-from-queue", adminHandler.RemoveFromQueue)

		// Test endpoint for payment simulation
		if cfg.Environment == "development" {
			e.Router.POST("/api/v1/test/simulate-payment", paymentHandler.SimulatePayment)
		}

		// Health check
		e.Router.GET("/health", func(e *core.RequestEvent) error {
			if err := utils.RedisHealthCheck(redisClient); err != nil {
				return e.JSON(503, map[string]string{
					"status": "unhealthy",
					"error":  err.Error(),
				})
			}
			return e.JSON(200, map[string]string{"status": "healthy"})
		})

		log.Println("Server routes registered")

		setupEventHooks(app, redisClient)

		return e.Next()
	})

	// Start server
	if err := app.Start(); err != nil {
		log.Fatal(err)
	}
	return nil
}

func syncActiveEventsToRedis(app *pocketbase.PocketBase, redisClient *redis.Client) {
	ctx := context.Background()

	// Get active events from database (latest PocketBase version)
	var records []dbx.NullStringMap
	if err := app.DB().NewQuery(
		"SELECT id FROM events WHERE status = 'publish'",
	).All(&records); err != nil {
		log.Printf("Error fetching active events: %v", err)
		return
	}

	// Clear existing active_events set
	redisClient.Del(ctx, "active_events")

	// Add active events to Redis set
	if len(records) > 0 {
		var eventIDs []interface{}
		for _, record := range records {
			if id := record["id"].String; id != "" {
				eventIDs = append(eventIDs, id)
			}
		}

		if len(eventIDs) > 0 {
			redisClient.SAdd(ctx, "active_events", eventIDs...)
			log.Printf("Synced %d active events to Redis", len(eventIDs))
		}
	}
}

func setupEventHooks(app *pocketbase.PocketBase, redisClient *redis.Client) {
	// Hook: OnRecordAfterCreateRequest for "events" collection
	// This hook fires AFTER a new 'event' record has been successfully created.
	app.OnRecordCreateRequest("events").BindFunc(func(e *core.RecordRequestEvent) error {
		// Use e.RequestContext to get the context associated with the current request.
		// This context will be cancelled if the client disconnects or the request times out.
		ctx := e.Request.Context() // Use the request context

		eventID := e.Record.Id
		eventStatus := e.Record.GetString("status")

		if eventStatus == "active" {
			// Add the event ID to the Redis set of active events
			if err := redisClient.SAdd(ctx, "active_events", eventID).Err(); err != nil {
				slog.Error("Failed to add new active event to Redis",
					"eventID", eventID,
					"error", err,
					"hook", "OnRecordAfterCreateRequest",
				)
				// Returning an error here would cause the HTTP request to fail.
				// For Redis sync, often we just log the error and let the request succeed.
				// Decide based on your application's error handling philosophy.
				return nil // Don't block the request if Redis sync fails
			}
			slog.Info("Added new active event to Redis", "eventID", eventID)
		} else {
			slog.Info("New event not active, skipping Redis add", "eventID", eventID, "status", eventStatus)
		}
		return nil
	})

	// Hook: OnRecordAfterUpdateRequest for "events" collection
	// This hook fires AFTER an 'event' record has been successfully updated.
	app.OnRecordUpdateRequest("events").BindFunc(func(e *core.RecordRequestEvent) error {
		ctx := e.Request.Context() // Use the request context

		eventID := e.Record.Id
		newStatus := e.Record.GetString("status")
		// If you need the *old* status, you can get it from e.Record.OriginalCopy()
		// oldStatus := e.Record.OriginalCopy().GetString("status")

		if newStatus == "active" {
			// If the event is now active, ensure it's in the Redis set
			if err := redisClient.SAdd(ctx, "active_events", eventID).Err(); err != nil {
				slog.Error("Failed to add updated active event to Redis",
					"eventID", eventID,
					"newStatus", newStatus,
					"error", err,
					"hook", "OnRecordAfterUpdateRequest",
				)
				return nil
			}
			slog.Info("Ensured event is active in Redis", "eventID", eventID, "status", newStatus)
		} else {
			// If the event is no longer active (e.g., draft, ended, cancelled), remove it from the Redis set
			if err := redisClient.SRem(ctx, "active_events", eventID).Err(); err != nil {
				slog.Error("Failed to remove non-active event from Redis",
					"eventID", eventID,
					"newStatus", newStatus,
					"error", err,
					"hook", "OnRecordAfterUpdateRequest",
				)
				return nil
			}
			slog.Info("Removed non-active event from Redis", "eventID", eventID, "status", newStatus)
		}
		return nil
	})

	// Hook: OnRecordAfterDeleteRequest for "events" collection
	// This hook fires AFTER an 'event' record has been successfully deleted.
	app.OnRecordDeleteRequest("events").BindFunc(func(e *core.RecordRequestEvent) error {
		ctx := e.Request.Context() // Use the request context

		eventID := e.Record.Id

		// Remove the deleted event ID from the Redis set
		if err := redisClient.SRem(ctx, "active_events", eventID).Err(); err != nil {
			slog.Error("Failed to remove deleted event from Redis",
				"eventID", eventID,
				"error", err,
				"hook", "OnRecordAfterDeleteRequest",
			)
			return nil
		}
		slog.Info("Removed deleted event from Redis", "eventID", eventID)
		return nil
	})
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
