package main

import (
	"context"
	"log"
	"ticket-system/config"
	"ticket-system/handlers"
	"ticket-system/services"

	"github.com/pocketbase/pocketbase"
	"github.com/pocketbase/pocketbase/core"
	pubnub "github.com/pubnub/go"
	"github.com/redis/go-redis/v9"
)

func main() {
	app := pocketbase.New()

	cfg := config.LoadConfig()

	redisClient := redis.NewClient(&redis.Options{
		Addr: cfg.RedisURL,
	})

	pnConfig := pubnub.NewConfig()
	pnConfig.PublishKey = cfg.PubNubPublishKey
	pnConfig.SubscribeKey = cfg.PubNubSubscribeKey

	pn := pubnub.NewPubNub(pnConfig)

	queueService := services.NewQueueService(redisClient, pn, cfg)
	_ = services.NewSeatService(redisClient)
	_ = services.NewPaymentService(redisClient, pn, queueService)

	queueHandler := handlers.NewQueueHandler(app, queueService)

	go queueService.UpdateQueuePositions(context.Background())

	go restoreQueueState(redisClient, queueService)

	app.OnBeforeServe().Add(func(e *core.ServeEvent) error {
		e.Router.POST("/api/queue/enter", queueHandler.EnterQueue)
		e.Router.GET("/api/queue/position", queueHandler.GetQueuePosition)

		return nil
	})

	if err := app.Start(); err != nil {
		log.Fatal(err)
	}
}

func restoreQueueState(redisClient *redis.Client, queueService *services.QueueService) {
	ctx := context.Background()

	keys, err := redisClient.Keys(ctx, "queue:waiting:*").Result()
	if err != nil {
		log.Printf("Error restoring queue state: %v", err)
		return
	}

	for _, key := range keys {
		eventID := key[len("queue:waiting:"):]

		queueLen, _ := redisClient.LLen(ctx, key).Result()
		if queueLen > 0 {
			go queueService.ProcessQueue(ctx, eventID)
		}
	}
}
