package security

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/labstack/echo/v5"
	"github.com/labstack/echo/v5/middleware"
	"github.com/redis/go-redis/v9"
)

type RateLimiter struct {
	redis *redis.Client
}

func NewRateLimiter(redisClient *redis.Client) *RateLimiter {
	return &RateLimiter{redis: redisClient}
}

// Rate limiting middleware for queue operations
func (r *RateLimiter) QueueRateLimit() echo.MiddlewareFunc {
	return middleware.RateLimiterWithConfig(middleware.RateLimiterConfig{
		Store: &redisStore{redis: r.redis},
		IdentifierExtractor: func(c echo.Context) (string, error) {
			// Rate limit by IP + User ID for authenticated requests
			userID := c.Get("user_id")
			if userID != nil {
				return fmt.Sprintf("user:%s", userID), nil
			}
			return c.RealIP(), nil
		},
		ErrorHandler: func(c echo.Context, err error) error {
			return c.JSON(429, map[string]string{
				"error": "Rate limit exceeded. Please try again later.",
			})
		},
	})
}

// Anti-bot protection
func (r *RateLimiter) AntiBotMiddleware() echo.MiddlewareFunc {
	return func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c echo.Context) error {
			// Check for bot patterns
			userAgent := c.Request().Header.Get("User-Agent")
			if r.isSuspiciousUserAgent(userAgent) {
				return c.JSON(403, map[string]string{
					"error": "Access denied",
				})
			}

			// Check request frequency
			ip := c.RealIP()
			key := fmt.Sprintf("antibot:%s", ip)

			count, err := r.redis.Incr(context.Background(), key).Result()
			if err == nil {
				if count == 1 {
					r.redis.Expire(context.Background(), key, time.Minute)
				}
				if count > 30 { // Max 30 requests per minute
					return c.JSON(429, map[string]string{
						"error": "Too many requests",
					})
				}
			}

			return next(c)
		}
	}
}

func (r *RateLimiter) isSuspiciousUserAgent(ua string) bool {
	suspicious := []string{"bot", "crawler", "spider", "scraper"}
	for _, pattern := range suspicious {
		if strings.Contains(strings.ToLower(ua), pattern) {
			return true
		}
	}
	return false
}
