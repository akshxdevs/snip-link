package server

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	redisdb "url-shortner/internal/redis"
)

type mockDB struct {
	store map[string]redisdb.URLStats
}

func newMockDB() *mockDB {
	return &mockDB{store: make(map[string]redisdb.URLStats)}
}

func (m *mockDB) Health() map[string]string {
	return map[string]string{"redis_status": "up"}
}

func (m *mockDB) CreateShortURL(_ context.Context, code, longURL string, ttl time.Duration) error {
	if _, ok := m.store[code]; ok {
		return redisdb.ErrConflict
	}

	stats := redisdb.URLStats{
		Code:      code,
		LongURL:   longURL,
		CreatedAt: time.Now().UTC(),
		Visits:    0,
	}
	if ttl > 0 {
		exp := time.Now().UTC().Add(ttl)
		stats.ExpiresAt = &exp
	}

	m.store[code] = stats
	return nil
}

func (m *mockDB) GetLongURL(_ context.Context, code string) (string, error) {
	stats, ok := m.store[code]
	if !ok {
		return "", redisdb.ErrNotFound
	}
	return stats.LongURL, nil
}

func (m *mockDB) IncrementVisits(_ context.Context, code string) (int64, error) {
	stats, ok := m.store[code]
	if !ok {
		return 0, redisdb.ErrNotFound
	}
	stats.Visits++
	m.store[code] = stats
	return stats.Visits, nil
}

func (m *mockDB) GetStats(_ context.Context, code string) (redisdb.URLStats, error) {
	stats, ok := m.store[code]
	if !ok {
		return redisdb.URLStats{}, redisdb.ErrNotFound
	}
	return stats, nil
}

func (m *mockDB) DeleteShortURL(_ context.Context, code string) error {
	if _, ok := m.store[code]; !ok {
		return redisdb.ErrNotFound
	}
	delete(m.store, code)
	return nil
}

func (m *mockDB) ShortCodeExists(_ context.Context, code string) (bool, error) {
	_, ok := m.store[code]
	return ok, nil
}

func TestCreateShortURLHandler(t *testing.T) {
	s := &Server{db: newMockDB()}
	h := s.RegisterRoutes()

	body := []byte(`{"url":"https://example.com/path"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/shorten", bytes.NewBuffer(body))
	req.Host = "short.local"
	res := httptest.NewRecorder()

	h.ServeHTTP(res, req)

	if res.Code != http.StatusCreated {
		t.Fatalf("expected status %d, got %d", http.StatusCreated, res.Code)
	}

	var out createShortURLResponse
	if err := json.Unmarshal(res.Body.Bytes(), &out); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
	if out.ShortCode == "" {
		t.Fatal("expected non-empty short code")
	}
	if out.LongURL != "https://example.com/path" {
		t.Fatalf("expected long_url to be preserved, got %s", out.LongURL)
	}
}

func TestRedirectHandler(t *testing.T) {
	db := newMockDB()
	if err := db.CreateShortURL(context.Background(), "abc1234", "https://example.com", 0); err != nil {
		t.Fatalf("setup failed: %v", err)
	}

	s := &Server{db: db}
	h := s.RegisterRoutes()

	req := httptest.NewRequest(http.MethodGet, "/abc1234", nil)
	res := httptest.NewRecorder()

	h.ServeHTTP(res, req)

	if res.Code != http.StatusFound {
		t.Fatalf("expected status %d, got %d", http.StatusFound, res.Code)
	}
	if loc := res.Header().Get("Location"); loc != "https://example.com" {
		t.Fatalf("expected redirect location https://example.com, got %s", loc)
	}

	stats, err := db.GetStats(context.Background(), "abc1234")
	if err != nil {
		t.Fatalf("failed to get stats: %v", err)
	}
	if stats.Visits != 1 {
		t.Fatalf("expected visits to be 1, got %d", stats.Visits)
	}
}

func TestURLStatsAndDelete(t *testing.T) {
	db := newMockDB()
	if err := db.CreateShortURL(context.Background(), "stat123", "https://example.com/stats", 0); err != nil {
		t.Fatalf("setup failed: %v", err)
	}
	if _, err := db.IncrementVisits(context.Background(), "stat123"); err != nil {
		t.Fatalf("setup increment failed: %v", err)
	}

	s := &Server{db: db}
	h := s.RegisterRoutes()

	statsReq := httptest.NewRequest(http.MethodGet, "/api/v1/urls/stat123", nil)
	statsRes := httptest.NewRecorder()
	h.ServeHTTP(statsRes, statsReq)

	if statsRes.Code != http.StatusOK {
		t.Fatalf("expected status %d, got %d", http.StatusOK, statsRes.Code)
	}

	var stats redisdb.URLStats
	if err := json.Unmarshal(statsRes.Body.Bytes(), &stats); err != nil {
		t.Fatalf("failed to parse stats response: %v", err)
	}
	if stats.Visits != 1 {
		t.Fatalf("expected visits to be 1, got %d", stats.Visits)
	}

	delReq := httptest.NewRequest(http.MethodDelete, "/api/v1/urls/stat123", nil)
	delRes := httptest.NewRecorder()
	h.ServeHTTP(delRes, delReq)

	if delRes.Code != http.StatusNoContent {
		t.Fatalf("expected status %d, got %d", http.StatusNoContent, delRes.Code)
	}

	_, err := db.GetLongURL(context.Background(), "stat123")
	if !errors.Is(err, redisdb.ErrNotFound) {
		t.Fatalf("expected ErrNotFound after delete, got %v", err)
	}
}
