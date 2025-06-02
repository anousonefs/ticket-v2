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

	"github.com/pocketbase/pocketbase/core"
	"github.com/pocketbase/pocketbase/plugins/migratecmd"
	pubnub "github.com/pubnub/go"
	"github.com/redis/go-redis/v9"

	"github.com/pocketbase/pocketbase" // Import the main PocketBase app
	// Alias 'm'
	// Alias for clarity, or just 'models'
	// For schema definitions
	// For types.Pointer
	// For daos.Dao if needed, but app.Dao() is common
)

//	func init() {
//		// Events collection
//		// The migration functions now receive 'app *pocketbase.PocketBase'
//		migrations.Register(func(app core.App) error {
//			dao := app.Dao() // Get the DAO instance from the app
//
//			collection := &pbmodels.Collection{}
//			collection.Name = "events"
//			collection.Type = pbmodels.CollectionTypeBase
//			collection.Schema = schema.NewSchema(
//				&schema.SchemaField{
//					Name:     "name",
//					Type:     schema.FieldTypeText,
//					Required: true,
//				},
//				&schema.SchemaField{
//					Name: "description",
//					Type: schema.FieldTypeEditor, // FieldTypeEditor implies rich text
//				},
//				&schema.SchemaField{
//					Name:     "start_date",
//					Type:     schema.FieldTypeDate, // Use FieldTypeDate for date only, or FieldTypeDateTime for date and time
//					Required: true,
//				},
//				&schema.SchemaField{
//					Name:     "venue",
//					Type:     schema.FieldTypeText,
//					Required: true,
//				},
//				&schema.SchemaField{
//					Name: "total_seats",
//					Type: schema.FieldTypeNumber,
//					Options: &schema.NumberOptions{
//						Min: types.Pointer(1.0), // Cleaner way to get a *float64
//					},
//				},
//				&schema.SchemaField{
//					Name: "max_processing_users",
//					Type: schema.FieldTypeNumber,
//					Options: &schema.NumberOptions{
//						Min: types.Pointer(1.0),  // Cleaner way to get a *float64
//						Max: types.Pointer(50.0), // Cleaner way to get a *float64
//					},
//				},
//				&schema.SchemaField{
//					Name: "status",
//					Type: schema.FieldTypeSelect,
//					Options: &schema.SelectOptions{
//						Values: []string{"draft", "published", "started", "ended"},
//						// Optional: Add other select options like `MaxSelect` or `Multiple`
//						// MaxSelect: types.Pointer(1), // Allow only one selection
//						// Multiple:  false,            // Not a multi-select
//					},
//				},
//				&schema.SchemaField{
//					Name: "image_url", // Added based on your previous React code
//					Type: schema.FieldTypeFile,
//					Options: &schema.FileOptions{
//						MaxSelect: 10,      // Allow up to 10 images
//						MaxSize:   5242880, // Max file size in bytes (e.g., 5MB)
//						MimeTypes: []string{"image/jpeg", "image/png", "image/gif"},
//					},
//				},
//			)
//
//			return dao.SaveCollection(collection)
//		}, func(app *pocketbase.PocketBase) error {
//			dao := app.Dao() // Get the DAO instance from the app
//			return dao.DeleteCollection(&pbmodels.Collection{Name: "events"})
//		})
//
//		// Tickets collection
//		migrations.Register(func(app core.App) error {
//			dao := app.Dao() // Get the DAO instance from the app
//
//			collection := &pbmodels.Collection{} // Use pbmodels for consistency
//			collection.Name = "tickets"
//			collection.Type = pbmodels.CollectionTypeBase
//			collection.Schema = schema.NewSchema(
//				&schema.SchemaField{
//					Name:     "event",
//					Type:     schema.FieldTypeRelation,
//					Required: true,
//					Options: &schema.RelationOptions{
//						CollectionId:  "events", // Correctly references the 'events' collection
//						CascadeDelete: false,    // Consider if you want tickets to be deleted when event is deleted
//						MinSelect:     nil,
//						MaxSelect:     types.Pointer(1), // One ticket belongs to one event
//					},
//				},
//				&schema.SchemaField{
//					Name:     "user",
//					Type:     schema.FieldTypeRelation,
//					Required: true,
//					Options: &schema.RelationOptions{
//						CollectionId:  "_pb_users_auth_", // Correctly references the built-in users collection
//						CascadeDelete: false,
//						MinSelect:     nil,
//						MaxSelect:     types.Pointer(1), // One ticket belongs to one user
//					},
//				},
//				&schema.SchemaField{
//					Name:     "seat_id",
//					Type:     schema.FieldTypeText,
//					Required: true,
//					// Optional: Add validation for seat_id format if needed
//					// Validators: []validator.Validator{validator.IsRegex(`^[A-Z]\d{1,3}$`)},
//				},
//				&schema.SchemaField{
//					Name: "price",
//					Type: schema.FieldTypeNumber,
//					Options: &schema.NumberOptions{
//						Min: types.Pointer(0.0), // Cleaner way to get a *float64
//					},
//				},
//				&schema.SchemaField{
//					Name: "status",
//					Type: schema.FieldTypeSelect,
//					Options: &schema.SelectOptions{
//						Values: []string{"locked", "sold", "cancelled"},
//						// MaxSelect: types.Pointer(1),
//					},
//				},
//				&schema.SchemaField{
//					Name: "payment_id",
//					Type: schema.FieldTypeText,
//					// Optional: Add validation for payment_id format if it's a specific ID
//				},
//				&schema.SchemaField{
//					Name: "qr_code",
//					Type: schema.FieldTypeFile, // QR code is typically an image file
//					Options: &schema.FileOptions{
//						MaxSelect: types.Pointer(1),       // One QR code per ticket
//						MaxSize:   types.Pointer(1048576), // Max file size (e.g., 1MB)
//						MimeTypes: []string{"image/png", "image/jpeg"},
//					},
//				},
//			)
//
//			return dao.SaveCollection(collection)
//		}, func(app *pocketbase.PocketBase) error {
//			dao := app.Dao() // Get the DAO instance from the app
//			return dao.DeleteCollection("tickets")
//		})
//	}
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
	app.OnServe().BindFunc(func(e *core.ServeEvent) error {
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

		return e.Next()
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
