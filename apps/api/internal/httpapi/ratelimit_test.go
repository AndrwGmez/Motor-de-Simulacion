package httpapi

import (
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/flowverse/flowverse-api/internal/auth"
	"github.com/flowverse/flowverse-api/internal/parser"
	"github.com/flowverse/flowverse-api/internal/runtime"
	"github.com/flowverse/flowverse-api/internal/store"
)

func TestRateLimiterWindowIsolationAndBound(t *testing.T) {
	current := time.Date(2026, 7, 30, 12, 0, 0, 0, time.UTC)
	limiter := newRateLimiter(2, time.Minute, func() time.Time { return current })
	policy := ratePolicy{Limit: 2, Window: time.Minute}
	if allowed, _ := limiter.allow("ip-a|login", policy); !allowed {
		t.Fatal("first request was rejected")
	}
	if allowed, _ := limiter.allow("ip-a|login", policy); !allowed {
		t.Fatal("second request was rejected")
	}
	if allowed, retry := limiter.allow("ip-a|login", policy); allowed || retry != time.Minute {
		t.Fatalf("third request allowed=%v retry=%s", allowed, retry)
	}
	if allowed, _ := limiter.allow("ip-a|register", policy); !allowed {
		t.Fatal("different route shared the same limit")
	}
	current = current.Add(time.Second)
	if allowed, _ := limiter.allow("ip-b|login", policy); !allowed {
		t.Fatal("different IP was rejected")
	}
	current = current.Add(time.Second)
	if allowed, _ := limiter.allow("ip-c|login", policy); !allowed {
		t.Fatal("bounded limiter rejected a new key")
	}
	limiter.mu.Lock()
	entryCount := len(limiter.entries)
	_, oldestStillPresent := limiter.entries["ip-a|register"]
	limiter.mu.Unlock()
	if entryCount > 2 || oldestStillPresent {
		t.Fatalf("entries=%d oldestPresent=%v; eviction did not bound memory", entryCount, oldestStillPresent)
	}
	current = current.Add(2 * time.Minute)
	if allowed, _ := limiter.allow("ip-a|login", policy); !allowed {
		t.Fatal("expired window was not reset")
	}
}

func TestHTTPRateLimitReturnsRetryAfterWithoutAffectingHealth(t *testing.T) {
	repository := store.NewMemory()
	authService := auth.New(repository, auth.Config{})
	server := New(repository, authService, parser.NewMock(), runtime.NewManager(repository), Config{PublicOrigin: "http://localhost:3000"})
	current := time.Date(2026, 7, 30, 12, 0, 0, 0, time.UTC)
	server.limiter = newRateLimiter(100, time.Minute, func() time.Time { return current })
	server.ratePolicies["auth.login"] = ratePolicy{Limit: 2, Window: time.Minute}
	server.ratePolicies["flows.parse_text"] = ratePolicy{Limit: 1, Window: time.Minute}
	client := &testClient{router: server.Router(), cookies: map[string]*http.Cookie{}}
	for attempt := 0; attempt < 2; attempt++ {
		response := client.request(t, http.MethodPost, "/v1/auth/login", map[string]any{"email": "missing@example.com", "password": "wrong"}, nil)
		if response.Code != http.StatusUnauthorized {
			t.Fatalf("attempt %d status=%d: %s", attempt, response.Code, response.Body.String())
		}
	}
	blocked := client.request(t, http.MethodPost, "/v1/auth/login", map[string]any{"email": "missing@example.com", "password": "wrong"}, nil)
	if blocked.Code != http.StatusTooManyRequests || blocked.Header().Get("Retry-After") != "60" {
		t.Fatalf("blocked status=%d retry=%q: %s", blocked.Code, blocked.Header().Get("Retry-After"), blocked.Body.String())
	}
	var apiError map[string]any
	_ = json.Unmarshal(blocked.Body.Bytes(), &apiError)
	if apiError["code"] != "rate_limit.exceeded" {
		t.Fatalf("error = %#v", apiError)
	}
	for attempt := 0; attempt < 20; attempt++ {
		if health := client.request(t, http.MethodGet, "/health/live", nil, nil); health.Code != http.StatusOK {
			t.Fatalf("health was rate limited at attempt %d", attempt)
		}
	}
	client.register(t, "limited-parser@example.com")
	parseBody := map[string]any{"text": "Validar un proceso suficientemente detallado.", "locale": "es"}
	if parsed := client.request(t, http.MethodPost, "/v1/flows/parse-text", parseBody, nil); parsed.Code != http.StatusOK {
		t.Fatalf("first parse status=%d: %s", parsed.Code, parsed.Body.String())
	}
	if parsed := client.request(t, http.MethodPost, "/v1/flows/parse-text", parseBody, nil); parsed.Code != http.StatusTooManyRequests {
		t.Fatalf("second parse status=%d: %s", parsed.Code, parsed.Body.String())
	}
	current = current.Add(time.Minute)
	afterWindow := client.request(t, http.MethodPost, "/v1/auth/login", map[string]any{"email": "missing@example.com", "password": "wrong"}, nil)
	if afterWindow.Code != http.StatusUnauthorized {
		t.Fatalf("window did not reset: %d %s", afterWindow.Code, afterWindow.Body.String())
	}
}
