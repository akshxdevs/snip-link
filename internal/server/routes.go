package server

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"math/big"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	"url-shortner/internal/database"
)

const (
	shortCodeLength = 7
	maxCodeAttempts = 10
)

var aliasPattern = regexp.MustCompile(`^[a-zA-Z0-9_-]{4,32}$`)

type createShortURLResponse struct {
	ShortCode string     `json:"short_code"`
	ShortURL  string     `json:"short_url"`
	LongURL   string     `json:"long_url"`
	ExpiresAt *time.Time `json:"expires_at,omitempty"`
}

type errorResponse struct {
	Error string `json:"error"`
}

func (s *Server) RegisterRoutes() http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("GET /", s.rootHandler)
	mux.HandleFunc("GET /health", s.healthHandler)

	mux.HandleFunc("POST /api/v1/shorten", s.createShortURLHandler)
	mux.HandleFunc("GET /api/v1/urls/{code}", s.urlStatsHandler)
	mux.HandleFunc("DELETE /api/v1/urls/{code}", s.deleteURLHandler)

	mux.HandleFunc("GET /{code}", s.redirectHandler)

	return s.corsMiddleware(mux)
}

func (s *Server) corsMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, DELETE, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Accept, Authorization, Content-Type, X-CSRF-Token")
		w.Header().Set("Access-Control-Allow-Credentials", "false")

		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}

		next.ServeHTTP(w, r)
	})
}

func (s *Server) rootHandler(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"service": "url-shortner",
		"version": "v1",
		"routes": []string{
			"POST /api/v1/shorten",
			"GET /{code}",
			"GET /api/v1/urls/{code}",
			"DELETE /api/v1/urls/{code}",
			"GET /health",
		},
	})
}

func (s *Server) createShortURLHandler(w http.ResponseWriter, r *http.Request) {
	type createShortURLRequest struct {
		URL            string `json:"url"`
		CustomAlias    string `json:"custom_alias,omitempty"`
		ExpirationDays int    `json:"expiration_days,omitempty"`
	}
	var req createShortURLRequest

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid json body")
		return
	}

	parsedURL, err := validateTargetURL(req.URL)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	if req.ExpirationDays < 0 {
		writeError(w, http.StatusBadRequest, "expiration_days must be >= 0")
		return
	}

	code, err := s.resolveShortCode(r.Context(), strings.TrimSpace(req.CustomAlias))
	if err != nil {
		if errors.Is(err, database.ErrConflict) {
			writeError(w, http.StatusConflict, "custom alias already exists")
			return
		}
		if strings.Contains(err.Error(), "custom_alias") {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		writeError(w, http.StatusInternalServerError, "failed to generate short code")
		return
	}

	var ttl time.Duration
	var expiresAt *time.Time
	if req.ExpirationDays > 0 {
		ttl = time.Duration(req.ExpirationDays) * 24 * time.Hour
		exp := time.Now().UTC().Add(ttl)
		expiresAt = &exp
	}

	log.Printf("URL Expiration: %d", req.ExpirationDays)

	if err := s.db.CreateShortURL(r.Context(), code, parsedURL.String(), ttl); err != nil {
		if errors.Is(err, database.ErrConflict) {
			writeError(w, http.StatusConflict, "short code already exists")
			return
		}
		writeError(w, http.StatusInternalServerError, "failed to store short URL")
		return
	}

	response := createShortURLResponse{
		ShortCode: code,
		ShortURL:  fmt.Sprintf("%s/%s", requestBaseURL(r), code),
		LongURL:   parsedURL.String(),
		ExpiresAt: expiresAt,
	}

	writeJSON(w, http.StatusCreated, response)
}

func (s *Server) redirectHandler(w http.ResponseWriter, r *http.Request) {
	code := strings.TrimSpace(r.PathValue("code"))
	if code == "" {
		writeError(w, http.StatusNotFound, "short code not found")
		return
	}

	target, err := s.db.GetLongURL(r.Context(), code)
	if err != nil {
		if errors.Is(err, database.ErrNotFound) {
			writeError(w, http.StatusNotFound, "short code not found")
			return
		}
		writeError(w, http.StatusInternalServerError, "failed to resolve short URL")
		return
	}

	if _, err := s.db.IncrementVisits(r.Context(), code); err != nil {
		log.Printf("failed to increment visits for %s: %v", code, err)
	}

	http.Redirect(w, r, target, http.StatusFound)
}

func (s *Server) urlStatsHandler(w http.ResponseWriter, r *http.Request) {
	code := strings.TrimSpace(r.PathValue("code"))
	if code == "" {
		writeError(w, http.StatusNotFound, "short code not found")
		return
	}

	stats, err := s.db.GetStats(r.Context(), code)
	if err != nil {
		if errors.Is(err, database.ErrNotFound) {
			writeError(w, http.StatusNotFound, "short code not found")
			return
		}
		writeError(w, http.StatusInternalServerError, "failed to fetch URL stats")
		return
	}

	writeJSON(w, http.StatusOK, stats)
}

func (s *Server) deleteURLHandler(w http.ResponseWriter, r *http.Request) {
	code := strings.TrimSpace(r.PathValue("code"))
	if code == "" {
		writeError(w, http.StatusNotFound, "short code not found")
		return
	}

	if err := s.db.DeleteShortURL(r.Context(), code); err != nil {
		if errors.Is(err, database.ErrNotFound) {
			writeError(w, http.StatusNotFound, "short code not found")
			return
		}
		writeError(w, http.StatusInternalServerError, "failed to delete short URL")
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) healthHandler(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, s.db.Health())
}

func (s *Server) resolveShortCode(ctx context.Context, customAlias string) (string, error) {
	if customAlias != "" {
		if !aliasPattern.MatchString(customAlias) {
			return "", fmt.Errorf("custom_alias must match %s", aliasPattern.String())
		}
		exists, err := s.db.ShortCodeExists(ctx, customAlias)
		if err != nil {
			return "", err
		}
		if exists {
			return "", database.ErrConflict
		}
		return customAlias, nil
	}

	for i := 0; i < maxCodeAttempts; i++ {
		candidate, err := generateShortCode(shortCodeLength)
		if err != nil {
			return "", err
		}

		exists, err := s.db.ShortCodeExists(ctx, candidate)
		if err != nil {
			return "", err
		}
		if !exists {
			return candidate, nil
		}
	}

	return "", errors.New("failed to allocate unique short code")
}

func validateTargetURL(raw string) (*url.URL, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return nil, errors.New("url is required")
	}

	parsed, err := url.Parse(trimmed)
	if err != nil {
		return nil, errors.New("invalid url")
	}

	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return nil, errors.New("url must start with http:// or https://")
	}

	if parsed.Host == "" {
		return nil, errors.New("url host is required")
	}

	return parsed, nil
}

func requestBaseURL(r *http.Request) string {
	scheme := "http"
	if forwardedProto := r.Header.Get("X-Forwarded-Proto"); forwardedProto != "" {
		scheme = forwardedProto
	} else if r.TLS != nil {
		scheme = "https"
	}

	return fmt.Sprintf("%s://%s", scheme, r.Host)
}

func generateShortCode(length int) (string, error) {
	const alphabet = "0123456789abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ"
	max := big.NewInt(int64(len(alphabet)))

	buf := make([]byte, length)
	for i := range buf {
		n, err := rand.Int(rand.Reader, max)
		if err != nil {
			return "", fmt.Errorf("generate short code: %w", err)
		}
		buf[i] = alphabet[n.Int64()]
	}

	return string(buf), nil
}

func writeError(w http.ResponseWriter, statusCode int, message string) {
	writeJSON(w, statusCode, errorResponse{Error: message})
}

func writeJSON(w http.ResponseWriter, statusCode int, payload any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(statusCode)
	if err := json.NewEncoder(w).Encode(payload); err != nil {
		log.Printf("failed to encode response: %v", err)
	}
}
