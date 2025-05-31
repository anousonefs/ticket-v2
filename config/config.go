package config

import (
	"os"
	"strconv"
	"time"
)

type Config struct {
	// Server configuration
	Port        string
	Environment string

	// Redis configuration
	RedisURL      string
	RedisPassword string
	RedisDB       int

	// PubNub configuration
	PubNubPublishKey   string
	PubNubSubscribeKey string
	PubNubSecretKey    string

	// Queue configuration
	MaxProcessingUsers  int
	QueuePositionUpdate time.Duration

	// Timeout configuration
	SeatLockTimeout   time.Duration
	PaymentTimeout    time.Duration
	ProcessingTimeout time.Duration

	// Cleanup configuration
	CleanupInterval  time.Duration
	InactiveQueueTTL time.Duration

	// Monitoring
	EnableMetrics bool
	MetricsPort   string
}

func LoadConfig() *Config {
	return &Config{
		// Server
		Port:        getEnv("PORT", "8090"),
		Environment: getEnv("ENVIRONMENT", "development"),

		// Redis
		RedisURL:      getEnv("REDIS_URL", "localhost:6379"),
		RedisPassword: getEnv("REDIS_PASSWORD", ""),
		RedisDB:       getEnvAsInt("REDIS_DB", 0),

		// PubNub
		PubNubPublishKey:   getEnv("PUBNUB_PUBLISH_KEY", ""),
		PubNubSubscribeKey: getEnv("PUBNUB_SUBSCRIBE_KEY", ""),
		PubNubSecretKey:    getEnv("PUBNUB_SECRET_KEY", ""),

		// Queue
		MaxProcessingUsers:  getEnvAsInt("MAX_PROCESSING_USERS", 10),
		QueuePositionUpdate: getEnvAsDuration("QUEUE_POSITION_UPDATE", "2s"),

		// Timeouts
		SeatLockTimeout:   getEnvAsDuration("SEAT_LOCK_TIMEOUT", "5m"),
		PaymentTimeout:    getEnvAsDuration("PAYMENT_TIMEOUT", "10m"),
		ProcessingTimeout: getEnvAsDuration("PROCESSING_TIMEOUT", "5m"),

		// Cleanup
		CleanupInterval:  getEnvAsDuration("CLEANUP_INTERVAL", "1h"),
		InactiveQueueTTL: getEnvAsDuration("INACTIVE_QUEUE_TTL", "1h"),

		// Monitoring
		EnableMetrics: getEnvAsBool("ENABLE_METRICS", true),
		MetricsPort:   getEnv("METRICS_PORT", "9090"),
	}
}

func getEnv(key, defaultValue string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return defaultValue
}

func getEnvAsInt(key string, defaultValue int) int {
	valueStr := getEnv(key, "")
	if value, err := strconv.Atoi(valueStr); err == nil {
		return value
	}
	return defaultValue
}

func getEnvAsBool(key string, defaultValue bool) bool {
	valueStr := getEnv(key, "")
	if value, err := strconv.ParseBool(valueStr); err == nil {
		return value
	}
	return defaultValue
}

func getEnvAsDuration(key string, defaultValue string) time.Duration {
	valueStr := getEnv(key, defaultValue)
	if duration, err := time.ParseDuration(valueStr); err == nil {
		return duration
	}
	// If parsing fails, try to parse default value
	duration, _ := time.ParseDuration(defaultValue)
	return duration
}
