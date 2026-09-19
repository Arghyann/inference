package main

import (
	"fmt"
	"net"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"
)

// IPRateLimiter implements an in-memory sliding window rate limiter per IP address.
type IPRateLimiter struct {
	mu       sync.Mutex
	clients  map[string][]time.Time
	limit    int
	window   time.Duration
	stopChan chan struct{}
}

// NewIPRateLimiter creates a new IPRateLimiter and starts a periodic cleanup worker.
func NewIPRateLimiter(limit int, window time.Duration) *IPRateLimiter {
	limiter := &IPRateLimiter{
		clients:  make(map[string][]time.Time),
		limit:    limit,
		window:   window,
		stopChan: make(chan struct{}),
	}

	// Periodic cleanup worker runs every 2 minutes to remove stale IPs and prevent memory leaks.
	go func() {
		ticker := time.NewTicker(2 * time.Minute)
		defer ticker.Stop()

		for {
			select {
			case <-ticker.C:
				limiter.cleanup()
			case <-limiter.stopChan:
				return
			}
		}
	}()

	return limiter
}

// Allow checks if the given IP address is allowed to make a request.
// Returns true if allowed, or false along with the remaining duration until the oldest request expires.
func (l *IPRateLimiter) Allow(ip string) (bool, time.Duration) {
	l.mu.Lock()
	defer l.mu.Unlock()

	now := time.Now()
	cutoff := now.Add(-l.window)

	timestamps := l.clients[ip]
	validIndex := 0
	for validIndex < len(timestamps) && !timestamps[validIndex].After(cutoff) {
		validIndex++
	}
	recent := timestamps[validIndex:]

	if len(recent) >= l.limit {
		oldest := recent[0]
		retryAfter := l.window - now.Sub(oldest)
		if retryAfter < 0 {
			retryAfter = 0
		}
		// Save trimmed list
		l.clients[ip] = recent
		return false, retryAfter
	}

	recent = append(recent, now)
	l.clients[ip] = recent
	return true, 0
}

func (l *IPRateLimiter) cleanup() {
	l.mu.Lock()
	defer l.mu.Unlock()

	now := time.Now()
	cutoff := now.Add(-l.window)

	for ip, timestamps := range l.clients {
		validIndex := 0
		for validIndex < len(timestamps) && !timestamps[validIndex].After(cutoff) {
			validIndex++
		}
		if validIndex >= len(timestamps) {
			delete(l.clients, ip)
		} else {
			l.clients[ip] = timestamps[validIndex:]
		}
	}
}

// Stop terminates the background cleanup goroutine (primarily for clean test teardowns).
func (l *IPRateLimiter) Stop() {
	select {
	case <-l.stopChan:
		// already closed
	default:
		close(l.stopChan)
	}
}

// GetClientIP extracts the real client IP address from standard proxy headers or RemoteAddr.
// If TRUST_PROXY is set to "false", proxy headers are ignored to prevent spoofing.
func GetClientIP(r *http.Request) string {
	if os.Getenv("TRUST_PROXY") != "false" {
		if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
			parts := strings.Split(xff, ",")
			ip := strings.TrimSpace(parts[0])
			if ip != "" {
				return ip
			}
		}

		if realIP := r.Header.Get("X-Real-IP"); realIP != "" {
			ip := strings.TrimSpace(realIP)
			if ip != "" {
				return ip
			}
		}
	}

	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return strings.TrimSpace(r.RemoteAddr)
	}
	return host
}

// RateLimitMiddleware wraps an HTTP handler with IP rate limiting.
func RateLimitMiddleware(limiter *IPRateLimiter, next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ip := GetClientIP(r)
		allowed, retryAfter := limiter.Allow(ip)
		if !allowed {
			seconds := int(retryAfter.Seconds()) + 1
			w.Header().Set("Retry-After", fmt.Sprintf("%d", seconds))
			writeJSON(w, http.StatusTooManyRequests, map[string]string{
				"error": fmt.Sprintf("Too many attempts. Please wait %d seconds and try again.", seconds),
			})
			return
		}
		next(w, r)
	}
}
