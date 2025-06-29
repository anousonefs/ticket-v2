package config

import (
	"os"
	"strconv"
	"ticket-system/internal/services/bank/jdb"
	"ticket-system/internal/services/bank/ldb"
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
	JDBConfig     *jdb.Config
	LDBConfig     *ldb.Config
}

// GetEnv GetEnv func for load config from .env file.
func GetEnv(key, fallback string) string {
	if value, ok := os.LookupEnv(key); ok {
		return value
	}
	return fallback
}

func LoadConfig() *Config {
	jdbCfg := jdb.Config{
		AID:         GetEnv("LAOQR_AID", "A005266284662577"),
		IIN:         GetEnv("LAOQR_IIN", "32170418"),
		ReceiverID:  os.Getenv("LAOQR_MID"),
		MCC:         GetEnv("LAOQR_MCC", "5251"),
		CCy:         GetEnv("LAOQR_CCY", "418"),
		Country:     GetEnv("LAOQR_COUNTRY", "LA"),
		MName:       os.Getenv("LAOQR_MNAME"),
		MCity:       os.Getenv("LAOQR_MCITY"),
		PNSubKey:    os.Getenv("PN_SUB_KEY"),
		PNSubSecret: os.Getenv("PN_SUB_SECRET"),
		PNUUID:      os.Getenv("PN_UUID"),
		PNChannel:   os.Getenv("PN_CHANNEL"),
		PNCipherKey: os.Getenv("PN_CIPHER_KEY"),
		BaseURL:     os.Getenv("JDB_BASE_URL"),
		PartnerID:   os.Getenv("JDB_PARTNER_ID"),
		ClientID:    os.Getenv("JDB_CLIENT_ID"),
		ClientKey:   os.Getenv("JDB_CLIENT_KEY"),
		HMACKey:     os.Getenv("JDB_HMAC_KEY"),
	}

	ldbCfg := ldb.Config{
		BaseURL:        os.Getenv("LDB_BASE_URL"),
		AccessTokenURL: os.Getenv("LDB_ACCESS_TOKEN_URL"),
		ClientID:       os.Getenv("LDB_CLIENT_ID"),
		ClientSecret:   os.Getenv("LDB_CLIENT_SECRET"),
		MerchantID:     os.Getenv("LDB_MERCHANT_ID"),
		PromotionCode:  os.Getenv("LDB_PROMOTION_CODE"),
		PartnerID:      os.Getenv("LDB_PARTNER_ID"),
		KeyID:          os.Getenv("LDB_KEY_ID"),
		HMacKey:        os.Getenv("LDB_HMAC_KEY"),
		SwitchBackURL:  os.Getenv("LDB_SWITCH_BACK_URL"),
	}

	return &Config{
		JDBConfig: &jdbCfg,
		LDBConfig: &ldbCfg,
		// Server
		Port:        getEnv("PORT", "8090"),
		Environment: getEnv("ENVIRONMENT", "development"),

		// Redis
		RedisURL:      getEnv("REDIS_URL", "localhost:6379"),
		RedisPassword: getEnv("REDIS_PASSWORD", ""),
		RedisDB:       getEnvAsInt("REDIS_DB", 0),

		// PubNub
		PubNubPublishKey:   getEnv("PN_PUBLISH_KEY", ""),
		PubNubSubscribeKey: getEnv("PN_SUBSCRIBE_KEY", ""),
		PubNubSecretKey:    getEnv("PN_SECRET_KEY", ""),

		// Queue
		MaxProcessingUsers:  getEnvAsInt("MAX_PROCESSING_USERS", 2),
		QueuePositionUpdate: getEnvAsDuration("QUEUE_POSITION_UPDATE", "5s"),

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
