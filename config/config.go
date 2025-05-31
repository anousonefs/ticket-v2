package config

import (
    "os"
    "time"
)

type Config struct {
    RedisURL            string
    PubNubPublishKey    string
    PubNubSubscribeKey  string
    MaxProcessingUsers  int
    SeatLockTimeout     time.Duration
    PaymentTimeout      time.Duration
    QueuePositionUpdate time.Duration
}

func LoadConfig() *Config {
    return &Config{
        RedisURL:            getEnv("REDIS_URL", "localhost:6379"),
        PubNubPublishKey:    getEnv("PUBNUB_PUBLISH_KEY", ""),
        PubNubSubscribeKey:  getEnv("PUBNUB_SUBSCRIBE_KEY", ""),
        MaxProcessingUsers:  10,
        SeatLockTimeout:     5 * time.Minute,
        PaymentTimeout:      10 * time.Minute,
        QueuePositionUpdate: 2 * time.Second,
    }
}

func getEnv(key, defaultValue string) string {
    if value := os.Getenv(key); value != "" {
        return value
    }
    return defaultValue
}
