package api

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestRateLimiterAllowsWithinWindow(t *testing.T) {
	limiter := newRateLimiter(2, time.Minute)
	now := time.Now()
	limiter.now = func() time.Time { return now }

	if !limiter.allow("client") {
		t.Fatal("first request should be allowed")
	}
	if !limiter.allow("client") {
		t.Fatal("second request should be allowed")
	}
	if limiter.allow("client") {
		t.Fatal("third request should be limited")
	}

	now = now.Add(2 * time.Minute)
	if !limiter.allow("client") {
		t.Fatal("request after the window should be allowed")
	}
}

func TestRateLimiterIsolatesClients(t *testing.T) {
	limiter := newRateLimiter(1, time.Minute)
	now := time.Now()
	limiter.now = func() time.Time { return now }

	if !limiter.allow("one") {
		t.Fatal("first client should be allowed")
	}
	if !limiter.allow("two") {
		t.Fatal("second client should be allowed")
	}
	if limiter.allow("one") {
		t.Fatal("first client should now be limited")
	}
}

func TestClientIP(t *testing.T) {
	request := httptest.NewRequest(http.MethodGet, "/", nil)
	request.RemoteAddr = "192.0.2.10:54321"
	if got := clientIP(request, false); got != "192.0.2.10" {
		t.Fatalf("unexpected client IP: %s", got)
	}

	request.Header.Set("X-Forwarded-For", "198.51.100.4, 192.0.2.10")
	if got := clientIP(request, false); got != "192.0.2.10" {
		t.Fatalf("proxy header must be ignored: %s", got)
	}
	if got := clientIP(request, true); got != "198.51.100.4" {
		t.Fatalf("unexpected forwarded client IP: %s", got)
	}
}
