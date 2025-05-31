package utils

import (
	"context"
	"fmt"
	"log"
	"time"

	"github.com/redis/go-redis/v9"
)

// NewRedisClient creates a new Redis client with connection pooling
func NewRedisClient(url string) *redis.Client {
	opts, err := redis.ParseURL(url)
	if err != nil {
		// Fall back to simple connection
		opts = &redis.Options{
			Addr: url,
		}
	}

	// Configure connection pool
	opts.PoolSize = 100
	opts.MinIdleConns = 10
	opts.MaxRetries = 3
	// opts.RetryBackoff = func(n int) time.Duration {
	// 	return time.Duration(n) * 100 * time.Millisecond
	// }

	client := redis.NewClient(opts)

	// Test connection
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := client.Ping(ctx).Err(); err != nil {
		log.Fatalf("Failed to connect to Redis: %v", err)
	}

	log.Println("Successfully connected to Redis")
	return client
}

// RedisHealthCheck performs a health check on Redis connection
func RedisHealthCheck(client *redis.Client) error {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	if err := client.Ping(ctx).Err(); err != nil {
		return fmt.Errorf("redis health check failed: %w", err)
	}

	return nil
}
