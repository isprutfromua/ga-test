package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/isprutfromua/ga-test/internal/metrics"
	"github.com/isprutfromua/ga-test/internal/models"
	"github.com/isprutfromua/ga-test/internal/service"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
)

type fakeSubscriptionService struct {
	subscribeErr     error
	confirmErr       error
	unsubscribeErr   error
	getSubsErr       error
	getSubsResult    []*models.Subscription
	subscribeCalls   int
	confirmCalls     int
	unsubscribeCalls int
}

func (f *fakeSubscriptionService) Subscribe(context.Context, string, string) error {
	f.subscribeCalls++
	return f.subscribeErr
}

func (f *fakeSubscriptionService) Confirm(context.Context, string) error {
	f.confirmCalls++
	return f.confirmErr
}

func (f *fakeSubscriptionService) Unsubscribe(context.Context, string) error {
	f.unsubscribeCalls++
	return f.unsubscribeErr
}

func (f *fakeSubscriptionService) GetSubscriptions(context.Context, string) ([]*models.Subscription, error) {
	if f.getSubsErr != nil {
		return nil, f.getSubsErr
	}
	return f.getSubsResult, nil
}

func TestSubscribeHandler(t *testing.T) {
	tests := []struct {
		name       string
		body       string
		ct         string
		svcErr     error
		wantStatus int
		wantBody   string
		wantCalls  int
	}{
		{name: "invalid JSON", body: `{"email":`, ct: "application/json", wantStatus: http.StatusBadRequest, wantBody: "invalid JSON body", wantCalls: 0},
		{name: "missing fields", body: `{"email":"","repo":""}`, ct: "application/json", wantStatus: http.StatusBadRequest, wantBody: "email and repo are required", wantCalls: 0},
		{name: "invalid email", body: `{"email":"invalid","repo":"owner/repo"}`, ct: "application/json", wantStatus: http.StatusBadRequest, wantBody: "invalid email address", wantCalls: 0},
		{name: "invalid repo error", body: `{"email":"u@example.com","repo":"owner/repo"}`, ct: "application/json", svcErr: service.ErrInvalidRepo, wantStatus: http.StatusBadRequest, wantBody: "invalid repo format", wantCalls: 1},
		{name: "repo not found error", body: `{"email":"u@example.com","repo":"owner/repo"}`, ct: "application/json", svcErr: service.ErrRepoNotFound, wantStatus: http.StatusNotFound, wantBody: "repository not found", wantCalls: 1},
		{name: "already exists error", body: `{"email":"u@example.com","repo":"owner/repo"}`, ct: "application/json", svcErr: service.ErrAlreadyExists, wantStatus: http.StatusConflict, wantBody: "already subscribed", wantCalls: 1},
		{name: "rate limited error", body: `{"email":"u@example.com","repo":"owner/repo"}`, ct: "application/json", svcErr: service.ErrRateLimited, wantStatus: http.StatusTooManyRequests, wantBody: "rate limit", wantCalls: 1},
		{name: "internal error", body: `{"email":"u@example.com","repo":"owner/repo"}`, ct: "application/json", svcErr: errors.New("boom"), wantStatus: http.StatusInternalServerError, wantBody: "internal server error", wantCalls: 1},
		{name: "success form encoded", body: "email=u%40example.com&repo=owner%2Frepo", ct: "application/x-www-form-urlencoded", wantStatus: http.StatusOK, wantBody: "subscription successful", wantCalls: 1},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			svc := &fakeSubscriptionService{subscribeErr: tt.svcErr}
			h := NewHandler(svc)

			req := httptest.NewRequest(http.MethodPost, "/api/subscribe", strings.NewReader(tt.body))
			req.Header.Set("Content-Type", tt.ct)
			rr := httptest.NewRecorder()

			h.Subscribe(rr, req)

			if rr.Code != tt.wantStatus {
				t.Fatalf("status = %d, want %d", rr.Code, tt.wantStatus)
			}
			if !strings.Contains(rr.Body.String(), tt.wantBody) {
				t.Fatalf("response body = %q, want contains %q", rr.Body.String(), tt.wantBody)
			}
			if svc.subscribeCalls != tt.wantCalls {
				t.Fatalf("Subscribe calls = %d, want %d", svc.subscribeCalls, tt.wantCalls)
			}
		})
	}
}

func TestConfirmSubscriptionHandler(t *testing.T) {
	tests := []struct {
		name         string
		token        string
		accept       string
		svcErr       error
		wantStatus   int
		wantBody     string
		wantLocation string
	}{
		{name: "missing token json", token: "", wantStatus: http.StatusBadRequest, wantBody: "token is required"},
		{name: "token not found json", token: "abc", svcErr: service.ErrTokenNotFound, wantStatus: http.StatusNotFound, wantBody: "token not found"},
		{name: "invalid token json", token: "abc", svcErr: errors.New("invalid"), wantStatus: http.StatusBadRequest, wantBody: "invalid token"},
		{name: "success json", token: "abc", wantStatus: http.StatusOK, wantBody: "confirmed successfully"},
		{name: "token not found html", token: "abc", accept: "text/html", svcErr: service.ErrTokenNotFound, wantStatus: http.StatusSeeOther, wantLocation: "/error.html?code=404&reason=Confirmation+token+not+found"},
		{name: "success html", token: "abc", accept: "text/html", wantStatus: http.StatusSeeOther, wantLocation: "/subscription.html?state=confirmed"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			svc := &fakeSubscriptionService{confirmErr: tt.svcErr}
			h := NewHandler(svc)
			req := httptest.NewRequest(http.MethodGet, "/api/confirm/"+tt.token, nil)
			if tt.accept != "" {
				req.Header.Set("Accept", tt.accept)
			}
			req = withURLParam(req, "token", tt.token)
			rr := httptest.NewRecorder()

			h.ConfirmSubscription(rr, req)

			if rr.Code != tt.wantStatus {
				t.Fatalf("status = %d, want %d", rr.Code, tt.wantStatus)
			}
			if tt.wantLocation != "" {
				if got := rr.Header().Get("Location"); got != tt.wantLocation {
					t.Fatalf("Location = %q, want %q", got, tt.wantLocation)
				}
			} else if !strings.Contains(rr.Body.String(), tt.wantBody) {
				t.Fatalf("body = %q, want contains %q", rr.Body.String(), tt.wantBody)
			}
		})
	}
}

func TestUnsubscribeHandler(t *testing.T) {
	tests := []struct {
		name         string
		token        string
		accept       string
		svcErr       error
		wantStatus   int
		wantBody     string
		wantLocation string
	}{
		{name: "missing token json", token: "", wantStatus: http.StatusBadRequest, wantBody: "token is required"},
		{name: "token not found json", token: "abc", svcErr: service.ErrTokenNotFound, wantStatus: http.StatusNotFound, wantBody: "token not found"},
		{name: "invalid token json", token: "abc", svcErr: errors.New("invalid"), wantStatus: http.StatusBadRequest, wantBody: "invalid token"},
		{name: "success json", token: "abc", wantStatus: http.StatusOK, wantBody: "unsubscribed successfully"},
		{name: "token not found html", token: "abc", accept: "text/html", svcErr: service.ErrTokenNotFound, wantStatus: http.StatusSeeOther, wantLocation: "/error.html?code=404&reason=Unsubscribe+token+not+found"},
		{name: "success html", token: "abc", accept: "text/html", wantStatus: http.StatusSeeOther, wantLocation: "/subscription.html?state=unsubscribed"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			svc := &fakeSubscriptionService{unsubscribeErr: tt.svcErr}
			h := NewHandler(svc)
			req := httptest.NewRequest(http.MethodGet, "/api/unsubscribe/"+tt.token, nil)
			if tt.accept != "" {
				req.Header.Set("Accept", tt.accept)
			}
			req = withURLParam(req, "token", tt.token)
			rr := httptest.NewRecorder()

			h.Unsubscribe(rr, req)

			if rr.Code != tt.wantStatus {
				t.Fatalf("status = %d, want %d", rr.Code, tt.wantStatus)
			}
			if tt.wantLocation != "" {
				if got := rr.Header().Get("Location"); got != tt.wantLocation {
					t.Fatalf("Location = %q, want %q", got, tt.wantLocation)
				}
			} else if !strings.Contains(rr.Body.String(), tt.wantBody) {
				t.Fatalf("body = %q, want contains %q", rr.Body.String(), tt.wantBody)
			}
		})
	}
}

func TestGetSubscriptionsHandler(t *testing.T) {
	tests := []struct {
		name       string
		query      string
		svcErr     error
		svcResult  []*models.Subscription
		wantStatus int
		wantBody   string
	}{
		{name: "missing email", query: "", wantStatus: http.StatusBadRequest, wantBody: "email query parameter is required"},
		{name: "invalid email", query: "?email=invalid", wantStatus: http.StatusBadRequest, wantBody: "invalid email address"},
		{name: "service error", query: "?email=u@example.com", svcErr: errors.New("db"), wantStatus: http.StatusInternalServerError, wantBody: "internal server error"},
		{name: "success", query: "?email=u@example.com", svcResult: []*models.Subscription{{ID: 1, Email: "u@example.com", Repo: "owner/repo"}}, wantStatus: http.StatusOK, wantBody: "owner/repo"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			svc := &fakeSubscriptionService{getSubsErr: tt.svcErr, getSubsResult: tt.svcResult}
			h := NewHandler(svc)
			req := httptest.NewRequest(http.MethodGet, "/api/subscriptions"+tt.query, nil)
			rr := httptest.NewRecorder()

			h.GetSubscriptions(rr, req)

			if rr.Code != tt.wantStatus {
				t.Fatalf("status = %d, want %d", rr.Code, tt.wantStatus)
			}
			if !strings.Contains(rr.Body.String(), tt.wantBody) {
				t.Fatalf("body = %q, want contains %q", rr.Body.String(), tt.wantBody)
			}
		})
	}
}

func TestAPIKeyMiddleware(t *testing.T) {
	mw := APIKeyMiddleware("secret")
	next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})
	h := mw(next)

	tests := []struct {
		name       string
		method     string
		path       string
		apiKey     string
		wantStatus int
	}{
		{name: "health is public", method: http.MethodGet, path: "/healthz", wantStatus: http.StatusNoContent},
		{name: "metrics is public", method: http.MethodGet, path: "/metrics", wantStatus: http.StatusNoContent},
		{name: "root get is public", method: http.MethodGet, path: "/", wantStatus: http.StatusNoContent},
		{name: "public confirm route", method: http.MethodGet, path: "/api/confirm/token", wantStatus: http.StatusNoContent},
		{name: "protected without key", method: http.MethodPost, path: "/api/subscribe", wantStatus: http.StatusUnauthorized},
		{name: "protected with key", method: http.MethodPost, path: "/api/subscribe", apiKey: "secret", wantStatus: http.StatusNoContent},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(tt.method, tt.path, nil)
			if tt.apiKey != "" {
				req.Header.Set("X-API-Key", tt.apiKey)
			}
			rr := httptest.NewRecorder()
			h.ServeHTTP(rr, req)
			if rr.Code != tt.wantStatus {
				t.Fatalf("status = %d, want %d", rr.Code, tt.wantStatus)
			}
		})
	}
}

func TestMetricsMiddleware(t *testing.T) {
	m := &metrics.Metrics{
		HTTPRequestDuration: prometheus.NewHistogramVec(
			prometheus.HistogramOpts{Name: "test_http_request_duration_seconds", Help: "test"},
			[]string{"method", "route", "status"},
		),
	}

	r := chi.NewRouter()
	r.Use(MetricsMiddleware(m))
	r.Get("/hello", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusCreated)
	})

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/hello", nil)
	r.ServeHTTP(rr, req)
	if rr.Code != http.StatusCreated {
		t.Fatalf("status = %d, want %d", rr.Code, http.StatusCreated)
	}

	if got := testutil.CollectAndCount(m.HTTPRequestDuration); got != 1 {
		t.Fatalf("histogram series count = %d, want 1", got)
	}
}

func TestHelpers(t *testing.T) {
	if !isValidEmail("u@example.com") {
		t.Fatal("isValidEmail() valid case returned false")
	}
	if isValidEmail("bad-email") {
		t.Fatal("isValidEmail() invalid case returned true")
	}

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Accept", "text/html,application/xhtml+xml")
	if !prefersHTML(req) {
		t.Fatal("prefersHTML() expected true")
	}

	rr := httptest.NewRecorder()
	writeJSON(rr, http.StatusAccepted, map[string]string{"ok": "1"})
	if rr.Code != http.StatusAccepted {
		t.Fatalf("writeJSON status = %d, want %d", rr.Code, http.StatusAccepted)
	}
	var body map[string]string
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatalf("writeJSON body unmarshal error: %v", err)
	}
	if body["ok"] != "1" {
		t.Fatalf("writeJSON body = %v, want ok=1", body)
	}
}

func withURLParam(req *http.Request, key, value string) *http.Request {
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add(key, value)
	ctx := context.WithValue(req.Context(), chi.RouteCtxKey, rctx)
	return req.WithContext(ctx)
}
