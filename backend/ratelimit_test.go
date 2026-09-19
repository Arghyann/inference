package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"
)

func TestRateLimiter_Basic(t *testing.T) {
	limiter := NewIPRateLimiter(3, 100*time.Millisecond)
	defer limiter.Stop()

	ip := "192.168.1.100"

	// First 3 requests should be allowed
	for i := 1; i <= 3; i++ {
		allowed, _ := limiter.Allow(ip)
		if !allowed {
			t.Fatalf("Request %d should have been allowed", i)
		}
	}

	// 4th request must be blocked
	allowed, retryAfter := limiter.Allow(ip)
	if allowed {
		t.Fatalf("4th request should have been blocked")
	}
	if retryAfter <= 0 {
		t.Fatalf("Expected retryAfter > 0, got %v", retryAfter)
	}
}

func TestRateLimiter_WindowExpiry(t *testing.T) {
	window := 80 * time.Millisecond
	limiter := NewIPRateLimiter(2, window)
	defer limiter.Stop()

	ip := "10.0.0.1"

	// Exhaust limit
	if ok, _ := limiter.Allow(ip); !ok {
		t.Fatal("1st request should be allowed")
	}
	if ok, _ := limiter.Allow(ip); !ok {
		t.Fatal("2nd request should be allowed")
	}
	if ok, _ := limiter.Allow(ip); ok {
		t.Fatal("3rd request should be blocked")
	}

	// Wait for window to expire
	time.Sleep(window + 20*time.Millisecond)

	// Should be allowed again
	if ok, _ := limiter.Allow(ip); !ok {
		t.Fatal("Request should be allowed after window expiration")
	}
}

func TestRateLimiter_MultipleIPs(t *testing.T) {
	limiter := NewIPRateLimiter(2, 500*time.Millisecond)
	defer limiter.Stop()

	ipA := "1.1.1.1"
	ipB := "2.2.2.2"

	// Exhaust IP A
	limiter.Allow(ipA)
	limiter.Allow(ipA)
	if ok, _ := limiter.Allow(ipA); ok {
		t.Fatal("IP A should be blocked")
	}

	// IP B should still be allowed
	if ok, _ := limiter.Allow(ipB); !ok {
		t.Fatal("IP B should be allowed independently of IP A")
	}
	if ok, _ := limiter.Allow(ipB); !ok {
		t.Fatal("IP B second request should be allowed")
	}
	if ok, _ := limiter.Allow(ipB); ok {
		t.Fatal("IP B third request should be blocked")
	}
}

func TestRateLimiter_Concurrent(t *testing.T) {
	limit := 50
	limiter := NewIPRateLimiter(limit, 1*time.Second)
	defer limiter.Stop()

	var wg sync.WaitGroup
	allowedCount := 0
	blockedCount := 0
	var mu sync.Mutex

	totalRequests := 100
	wg.Add(totalRequests)

	for i := 0; i < totalRequests; i++ {
		go func() {
			defer wg.Done()
			allowed, _ := limiter.Allow("shared-ip")
			mu.Lock()
			if allowed {
				allowedCount++
			} else {
				blockedCount++
			}
			mu.Unlock()
		}()
	}

	wg.Wait()

	if allowedCount != limit {
		t.Fatalf("Expected exactly %d allowed requests, got %d", limit, allowedCount)
	}
	if blockedCount != (totalRequests - limit) {
		t.Fatalf("Expected exactly %d blocked requests, got %d", totalRequests-limit, blockedCount)
	}
}

func TestGetClientIP(t *testing.T) {
	tests := []struct {
		name       string
		headers    map[string]string
		remoteAddr string
		expectedIP string
	}{
		{
			name:       "X-Forwarded-For single IP",
			headers:    map[string]string{"X-Forwarded-For": "203.0.113.195"},
			remoteAddr: "127.0.0.1:8080",
			expectedIP: "203.0.113.195",
		},
		{
			name:       "X-Forwarded-For multi-proxy (first client IP)",
			headers:    map[string]string{"X-Forwarded-For": "203.0.113.195, 70.41.3.18, 150.172.238.178"},
			remoteAddr: "127.0.0.1:8080",
			expectedIP: "203.0.113.195",
		},
		{
			name:       "X-Real-IP fallback",
			headers:    map[string]string{"X-Real-IP": "198.51.100.42"},
			remoteAddr: "127.0.0.1:8080",
			expectedIP: "198.51.100.42",
		},
		{
			name:       "RemoteAddr fallback with port",
			headers:    map[string]string{},
			remoteAddr: "192.0.2.1:54321",
			expectedIP: "192.0.2.1",
		},
		{
			name:       "RemoteAddr without port",
			headers:    map[string]string{},
			remoteAddr: "192.0.2.1",
			expectedIP: "192.0.2.1",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest("GET", "/test", nil)
			req.RemoteAddr = tc.remoteAddr
			for k, v := range tc.headers {
				req.Header.Set(k, v)
			}
			ip := GetClientIP(req)
			if ip != tc.expectedIP {
				t.Fatalf("Expected IP %q, got %q", tc.expectedIP, ip)
			}
		})
	}
}

func TestRateLimitMiddleware_HTTP(t *testing.T) {
	limiter := NewIPRateLimiter(2, 500*time.Millisecond)
	defer limiter.Stop()

	handlerCalls := 0
	dummyHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		handlerCalls++
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	})

	wrapped := RateLimitMiddleware(limiter, dummyHandler)

	// 1st request -> 200 OK
	req1 := httptest.NewRequest("POST", "/api/auth/login", nil)
	req1.RemoteAddr = "5.5.5.5:12345"
	rec1 := httptest.NewRecorder()
	wrapped.ServeHTTP(rec1, req1)
	if rec1.Code != http.StatusOK {
		t.Fatalf("Req 1: expected status 200, got %d", rec1.Code)
	}

	// 2nd request -> 200 OK
	req2 := httptest.NewRequest("POST", "/api/auth/login", nil)
	req2.RemoteAddr = "5.5.5.5:12345"
	rec2 := httptest.NewRecorder()
	wrapped.ServeHTTP(rec2, req2)
	if rec2.Code != http.StatusOK {
		t.Fatalf("Req 2: expected status 200, got %d", rec2.Code)
	}

	// 3rd request -> 429 Too Many Requests
	req3 := httptest.NewRequest("POST", "/api/auth/login", nil)
	req3.RemoteAddr = "5.5.5.5:12345"
	rec3 := httptest.NewRecorder()
	wrapped.ServeHTTP(rec3, req3)
	if rec3.Code != http.StatusTooManyRequests {
		t.Fatalf("Req 3: expected status 429, got %d", rec3.Code)
	}

	retryAfter := rec3.Header().Get("Retry-After")
	if retryAfter == "" {
		t.Fatal("Expected Retry-After header to be present on 429 response")
	}

	var errBody map[string]string
	if err := json.NewDecoder(rec3.Body).Decode(&errBody); err != nil {
		t.Fatalf("Failed to decode 429 JSON response: %v", err)
	}
	if errBody["error"] == "" {
		t.Fatal("Expected non-empty error message in 429 response")
	}

	if handlerCalls != 2 {
		t.Fatalf("Expected handler to be called 2 times, got %d", handlerCalls)
	}
}

func TestRateLimiter_Cleanup(t *testing.T) {
	window := 30 * time.Millisecond
	limiter := NewIPRateLimiter(2, window)
	defer limiter.Stop()

	limiter.Allow("stale-ip-1")
	limiter.Allow("stale-ip-2")

	if len(limiter.clients) != 2 {
		t.Fatalf("Expected 2 clients in map, got %d", len(limiter.clients))
	}

	// Wait until timestamps become stale
	time.Sleep(window + 10*time.Millisecond)

	// Trigger manual cleanup
	limiter.cleanup()

	if len(limiter.clients) != 0 {
		t.Fatalf("Expected 0 clients in map after cleanup, got %d", len(limiter.clients))
	}
}

func TestRateLimiter_BurstStress(t *testing.T) {
	limiter := NewIPRateLimiter(20, 200*time.Millisecond)
	defer limiter.Stop()

	var wg sync.WaitGroup
	allowed := 0
	denied := 0
	var mu sync.Mutex

	// 100 concurrent requests from the same IP
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			ok, _ := limiter.Allow("stress-ip")
			mu.Lock()
			if ok {
				allowed++
			} else {
				denied++
			}
			mu.Unlock()
		}()
	}
	wg.Wait()

	if allowed != 20 {
		t.Fatalf("Expected exactly 20 allowed, got %d", allowed)
	}
	if denied != 80 {
		t.Fatalf("Expected exactly 80 denied, got %d", denied)
	}

	// Wait for window to slide
	time.Sleep(250 * time.Millisecond)

	// Should be able to make another request
	ok, _ := limiter.Allow("stress-ip")
	if !ok {
		t.Fatal("Expected request to be allowed after window slide")
	}
}
