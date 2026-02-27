package database

import (
	"context"
	"errors"
	"log"
	"testing"
	"time"

	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/modules/redis"
)

var integrationReady bool

func tryStartRedisContainer() (func(context.Context, ...testcontainers.TerminateOption) error, error) {
	defer func() {
		if r := recover(); r != nil {
			log.Printf("integration tests skipped: Docker unavailable (%v)", r)
		}
	}()
	return mustStartRedisContainer()
}

func mustStartRedisContainer() (func(context.Context, ...testcontainers.TerminateOption) error, error) {
	dbContainer, err := redis.Run(
		context.Background(),
		"docker.io/redis:7.2.4",
		redis.WithSnapshotting(10, 1),
		redis.WithLogLevel(redis.LogLevelVerbose),
	)
	if err != nil {
		return nil, err
	}

	dbHost, err := dbContainer.Host(context.Background())
	if err != nil {
		return dbContainer.Terminate, err
	}

	dbPort, err := dbContainer.MappedPort(context.Background(), "6379/tcp")
	if err != nil {
		return dbContainer.Terminate, err
	}

	address = dbHost
	port = dbPort.Port()
	database = "0"
	password = ""

	return dbContainer.Terminate, nil
}

func TestMain(m *testing.M) {
	teardown, err := tryStartRedisContainer()
	if err != nil {
		log.Printf("integration tests skipped: could not start redis container: %v", err)
		integrationReady = false
		m.Run()
		return
	}
	if teardown == nil {
		integrationReady = false
		m.Run()
		return
	}
	integrationReady = true

	m.Run()

	if teardown != nil && teardown(context.Background()) != nil {
		log.Printf("could not teardown redis container")
	}
}

func requireIntegration(t *testing.T) {
	t.Helper()
	if !integrationReady {
		t.Skip("integration environment unavailable")
	}
}

func TestNew(t *testing.T) {
	requireIntegration(t)

	srv := New()
	if srv == nil {
		t.Fatal("New() returned nil")
	}
}

func TestHealth(t *testing.T) {
	requireIntegration(t)

	srv := New()
	stats := srv.Health()

	if stats["redis_status"] != "up" {
		t.Fatalf("expected status to be up, got %s", stats["redis_status"])
	}
	if _, ok := stats["redis_version"]; !ok {
		t.Fatalf("expected redis_version to be present")
	}
}

func TestCRUDAndVisits(t *testing.T) {
	requireIntegration(t)

	srv := New()
	ctx := context.Background()

	if err := srv.CreateShortURL(ctx, "abc1234", "https://example.com", time.Hour); err != nil {
		t.Fatalf("CreateShortURL failed: %v", err)
	}

	if err := srv.CreateShortURL(ctx, "abc1234", "https://example.com/dup", time.Hour); !errors.Is(err, ErrConflict) {
		t.Fatalf("expected ErrConflict, got %v", err)
	}

	longURL, err := srv.GetLongURL(ctx, "abc1234")
	if err != nil {
		t.Fatalf("GetLongURL failed: %v", err)
	}
	if longURL != "https://example.com" {
		t.Fatalf("unexpected long URL: %s", longURL)
	}

	visits, err := srv.IncrementVisits(ctx, "abc1234")
	if err != nil {
		t.Fatalf("IncrementVisits failed: %v", err)
	}
	if visits != 1 {
		t.Fatalf("expected visits=1, got %d", visits)
	}

	stats, err := srv.GetStats(ctx, "abc1234")
	if err != nil {
		t.Fatalf("GetStats failed: %v", err)
	}
	if stats.Visits != 1 {
		t.Fatalf("expected stats visits=1, got %d", stats.Visits)
	}
	if stats.ExpiresAt == nil {
		t.Fatal("expected expires_at to be set")
	}

	if err := srv.DeleteShortURL(ctx, "abc1234"); err != nil {
		t.Fatalf("DeleteShortURL failed: %v", err)
	}

	if _, err := srv.GetLongURL(ctx, "abc1234"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected ErrNotFound after delete, got %v", err)
	}
}
