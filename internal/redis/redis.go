package redisdb

import (
	"context"
	"errors"
	"fmt"
	"log"
	"math"
	"os"
	"strconv"
	"strings"
	"time"

	_ "github.com/joho/godotenv/autoload"
	"github.com/redis/go-redis/v9"
)

const (
	shortURLKeyPrefix = "short:url:"
)

var (
	ErrNotFound = errors.New("short url not found")
	ErrConflict = errors.New("short code already exists")
)

type URLStats struct {
	Code      string     `json:"code"`
	LongURL   string     `json:"long_url"`
	CreatedAt time.Time  `json:"created_at"`
	Visits    int64      `json:"visits"`
	ExpiresAt *time.Time `json:"expires_at,omitempty"`
}

type Service interface {
	Health() map[string]string
	CreateShortURL(ctx context.Context, code, longURL string, ttl time.Duration) error
	GetLongURL(ctx context.Context, code string) (string, error)
	IncrementVisits(ctx context.Context, code string) (int64, error)
	GetStats(ctx context.Context, code string) (URLStats, error)
	DeleteShortURL(ctx context.Context, code string) error
	ShortCodeExists(ctx context.Context, code string) (bool, error)
}

type service struct {
	db *redis.Client
}

var (
	address  = os.Getenv("BLUEPRINT_DB_ADDRESS")
	port     = os.Getenv("BLUEPRINT_DB_PORT")
	password = os.Getenv("BLUEPRINT_DB_PASSWORD")
	database = os.Getenv("BLUEPRINT_DB_DATABASE")
)

func New() Service {
	num, err := strconv.Atoi(database)
	if err != nil {
		log.Fatalf("database incorrect %v", err)
	}

	fullAddress := fmt.Sprintf("%s:%s", address, port)

	rdb := redis.NewClient(&redis.Options{
		Addr:     fullAddress,
		Password: password,
		DB:       num,
	})

	return &service{db: rdb}
}

func shortURLKey(code string) string {
	return shortURLKeyPrefix + code
}

func (s *service) CreateShortURL(ctx context.Context, code, longURL string, ttl time.Duration) error {
	key := shortURLKey(code)
	createdAt := time.Now().UTC().Format(time.RFC3339Nano)

	created, err := s.db.HSetNX(ctx, key, "url", longURL).Result()
	if err != nil {
		return fmt.Errorf("create short url: %w", err)
	}
	if !created {
		return ErrConflict
	}

	if _, err := s.db.HSet(ctx, key,
		"created_at", createdAt,
		"visits", 0,
	).Result(); err != nil {
		return fmt.Errorf("create short url metadata: %w", err)
	}

	if ttl > 0 {
		if err := s.db.Expire(ctx, key, ttl).Err(); err != nil {
			return fmt.Errorf("set short url ttl: %w", err)
		}
	}

	return nil
}

func (s *service) GetLongURL(ctx context.Context, code string) (string, error) {
	url, err := s.db.HGet(ctx, shortURLKey(code), "url").Result()
	if err != nil {
		if errors.Is(err, redis.Nil) {
			return "", ErrNotFound
		}
		return "", fmt.Errorf("get long url: %w", err)
	}

	return url, nil
}

func (s *service) IncrementVisits(ctx context.Context, code string) (int64, error) {
	exists, err := s.ShortCodeExists(ctx, code)
	if err != nil {
		return 0, err
	}
	if !exists {
		return 0, ErrNotFound
	}

	visits, err := s.db.HIncrBy(ctx, shortURLKey(code), "visits", 1).Result()
	if err != nil {
		return 0, fmt.Errorf("increment visits: %w", err)
	}
	return visits, nil
}

func (s *service) GetStats(ctx context.Context, code string) (URLStats, error) {
	key := shortURLKey(code)
	values, err := s.db.HGetAll(ctx, key).Result()
	if err != nil {
		return URLStats{}, fmt.Errorf("get stats: %w", err)
	}
	if len(values) == 0 {
		return URLStats{}, ErrNotFound
	}

	createdAt, err := time.Parse(time.RFC3339Nano, values["created_at"])
	if err != nil {
		return URLStats{}, fmt.Errorf("parse created_at: %w", err)
	}

	visits, err := strconv.ParseInt(values["visits"], 10, 64)
	if err != nil {
		return URLStats{}, fmt.Errorf("parse visits: %w", err)
	}

	ttl, err := s.db.TTL(ctx, key).Result()
	if err != nil {
		return URLStats{}, fmt.Errorf("get ttl: %w", err)
	}

	stats := URLStats{
		Code:      code,
		LongURL:   values["url"],
		CreatedAt: createdAt,
		Visits:    visits,
	}

	if ttl > 0 {
		expiresAt := time.Now().UTC().Add(ttl)
		stats.ExpiresAt = &expiresAt
	}

	return stats, nil
}

func (s *service) DeleteShortURL(ctx context.Context, code string) error {
	removed, err := s.db.Del(ctx, shortURLKey(code)).Result()
	if err != nil {
		return fmt.Errorf("delete short url: %w", err)
	}
	if removed == 0 {
		return ErrNotFound
	}

	return nil
}

func (s *service) ShortCodeExists(ctx context.Context, code string) (bool, error) {
	exists, err := s.db.Exists(ctx, shortURLKey(code)).Result()
	if err != nil {
		return false, fmt.Errorf("check short code exists: %w", err)
	}
	return exists == 1, nil
}

// Health returns the health status and statistics of the Redis server.
func (s *service) Health() map[string]string {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	stats := make(map[string]string)
	stats = s.checkRedisHealth(ctx, stats)

	return stats
}

// checkRedisHealth checks the health of the Redis server and adds the relevant statistics to the stats map.
func (s *service) checkRedisHealth(ctx context.Context, stats map[string]string) map[string]string {
	pong, err := s.db.Ping(ctx).Result()
	if err != nil {
		stats["redis_status"] = "down"
		stats["redis_message"] = fmt.Sprintf("Redis ping failed: %v", err)
		return stats
	}

	stats["redis_status"] = "up"
	stats["redis_message"] = "It's healthy"
	stats["redis_ping_response"] = pong

	info, err := s.db.Info(ctx).Result()
	if err != nil {
		stats["redis_message"] = fmt.Sprintf("Failed to retrieve Redis info: %v", err)
		return stats
	}

	redisInfo := parseRedisInfo(info)
	poolStats := s.db.PoolStats()

	stats["redis_version"] = redisInfo["redis_version"]
	stats["redis_mode"] = redisInfo["redis_mode"]
	stats["redis_connected_clients"] = redisInfo["connected_clients"]
	stats["redis_used_memory"] = redisInfo["used_memory"]
	stats["redis_used_memory_peak"] = redisInfo["used_memory_peak"]
	stats["redis_uptime_in_seconds"] = redisInfo["uptime_in_seconds"]
	stats["redis_hits_connections"] = strconv.FormatUint(uint64(poolStats.Hits), 10)
	stats["redis_misses_connections"] = strconv.FormatUint(uint64(poolStats.Misses), 10)
	stats["redis_timeouts_connections"] = strconv.FormatUint(uint64(poolStats.Timeouts), 10)
	stats["redis_total_connections"] = strconv.FormatUint(uint64(poolStats.TotalConns), 10)
	stats["redis_idle_connections"] = strconv.FormatUint(uint64(poolStats.IdleConns), 10)
	stats["redis_stale_connections"] = strconv.FormatUint(uint64(poolStats.StaleConns), 10)
	stats["redis_max_memory"] = redisInfo["maxmemory"]

	activeConns := uint64(math.Max(float64(poolStats.TotalConns-poolStats.IdleConns), 0))
	stats["redis_active_connections"] = strconv.FormatUint(activeConns, 10)

	poolSize := s.db.Options().PoolSize
	connectedClients, _ := strconv.Atoi(redisInfo["connected_clients"])
	if poolSize > 0 {
		poolSizePercentage := float64(connectedClients) / float64(poolSize) * 100
		stats["redis_pool_size_percentage"] = fmt.Sprintf("%.2f%%", poolSizePercentage)
	}

	return s.evaluateRedisStats(redisInfo, stats)
}

// evaluateRedisStats evaluates the Redis server statistics and updates the stats map with relevant messages.
func (s *service) evaluateRedisStats(redisInfo, stats map[string]string) map[string]string {
	poolSize := s.db.Options().PoolSize
	poolStats := s.db.PoolStats()
	connectedClients, _ := strconv.Atoi(redisInfo["connected_clients"])
	highConnectionThreshold := int(float64(poolSize) * 0.8)

	if connectedClients > highConnectionThreshold {
		stats["redis_message"] = "Redis has a high number of connected clients"
	}

	minStaleConnectionsThreshold := 500
	if int(poolStats.StaleConns) > minStaleConnectionsThreshold {
		stats["redis_message"] = fmt.Sprintf("Redis has %d stale connections.", poolStats.StaleConns)
	}

	usedMemory, _ := strconv.ParseInt(redisInfo["used_memory"], 10, 64)
	maxMemory, _ := strconv.ParseInt(redisInfo["maxmemory"], 10, 64)
	if maxMemory > 0 {
		usedMemoryPercentage := float64(usedMemory) / float64(maxMemory) * 100
		if usedMemoryPercentage >= 90 {
			stats["redis_message"] = "Redis is using a significant amount of memory"
		}
	}

	uptimeInSeconds, _ := strconv.ParseInt(redisInfo["uptime_in_seconds"], 10, 64)
	if uptimeInSeconds < 3600 {
		stats["redis_message"] = "Redis has been recently restarted"
	}

	idleConns := int(poolStats.IdleConns)
	highIdleConnectionThreshold := int(float64(poolSize) * 0.7)
	if idleConns > highIdleConnectionThreshold {
		stats["redis_message"] = "Redis has a high number of idle connections"
	}

	if poolSize > 0 {
		poolUtilization := float64(poolStats.TotalConns-poolStats.IdleConns) / float64(poolSize) * 100
		highPoolUtilizationThreshold := 90.0
		if poolUtilization > highPoolUtilizationThreshold {
			stats["redis_message"] = "Redis connection pool utilization is high"
		}
	}

	return stats
}

// parseRedisInfo parses the Redis info response and returns a map of key-value pairs.
func parseRedisInfo(info string) map[string]string {
	result := make(map[string]string)
	lines := strings.Split(info, "\r\n")
	for _, line := range lines {
		if strings.Contains(line, ":") {
			parts := strings.Split(line, ":")
			key := strings.TrimSpace(parts[0])
			value := strings.TrimSpace(parts[1])
			result[key] = value
		}
	}
	return result
}
