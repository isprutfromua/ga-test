package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/isprutfromua/ga-test/internal/metrics"
	"github.com/isprutfromua/ga-test/internal/models"
	"github.com/isprutfromua/ga-test/internal/service"
	"github.com/prometheus/client_golang/prometheus"
)

type contractService struct {
	subscribeErr   error
	confirmErr     error
	unsubscribeErr error
	getSubsErr     error
	getSubsResult  []*models.Subscription
}

func (s *contractService) Subscribe(context.Context, string, string) error {
	return s.subscribeErr
}

func (s *contractService) Confirm(context.Context, string) error {
	return s.confirmErr
}

func (s *contractService) Unsubscribe(context.Context, string) error {
	return s.unsubscribeErr
}

func (s *contractService) GetSubscriptions(context.Context, string) ([]*models.Subscription, error) {
	if s.getSubsErr != nil {
		return nil, s.getSubsErr
	}
	return s.getSubsResult, nil
}

func newContractRouter(svc service.SubscriptionService, apiKey string) http.Handler {
	h := NewHandler(svc)
	m := &metrics.Metrics{
		HTTPRequestDuration: prometheus.NewHistogramVec(
			prometheus.HistogramOpts{Name: "test_contract_http_request_duration_seconds", Help: "test"},
			[]string{"method", "route", "status"},
		),
	}
	return NewRouter(h, m, apiKey, ".")
}

func TestSwaggerContractStatusMatrix(t *testing.T) {
	tests := []struct {
		name        string
		svc         *contractService
		method      string
		path        string
		body        string
		contentType string
		apiKey      string
		wantStatus  int
	}{
		{
			name:        "subscribe success",
			svc:         &contractService{},
			method:      http.MethodPost,
			path:        "/api/subscribe",
			body:        "email=u%40example.com&repo=owner%2Frepo",
			contentType: "application/x-www-form-urlencoded",
			apiKey:      "secret",
			wantStatus:  http.StatusOK,
		},
		{
			name:        "subscribe invalid repo format",
			svc:         &contractService{subscribeErr: service.ErrInvalidRepo},
			method:      http.MethodPost,
			path:        "/api/subscribe",
			body:        `{"email":"u@example.com","repo":"bad"}`,
			contentType: "application/json",
			apiKey:      "secret",
			wantStatus:  http.StatusBadRequest,
		},
		{
			name:        "subscribe repo not found",
			svc:         &contractService{subscribeErr: service.ErrRepoNotFound},
			method:      http.MethodPost,
			path:        "/api/subscribe",
			body:        `{"email":"u@example.com","repo":"owner/repo"}`,
			contentType: "application/json",
			apiKey:      "secret",
			wantStatus:  http.StatusNotFound,
		},
		{
			name:        "subscribe duplicate",
			svc:         &contractService{subscribeErr: service.ErrAlreadyExists},
			method:      http.MethodPost,
			path:        "/api/subscribe",
			body:        `{"email":"u@example.com","repo":"owner/repo"}`,
			contentType: "application/json",
			apiKey:      "secret",
			wantStatus:  http.StatusConflict,
		},
		{
			name:       "confirm success",
			svc:        &contractService{},
			method:     http.MethodGet,
			path:       "/api/confirm/token",
			wantStatus: http.StatusOK,
		},
		{
			name:       "confirm token not found",
			svc:        &contractService{confirmErr: service.ErrTokenNotFound},
			method:     http.MethodGet,
			path:       "/api/confirm/token",
			wantStatus: http.StatusNotFound,
		},
		{
			name:       "confirm invalid token",
			svc:        &contractService{confirmErr: errors.New("invalid token format")},
			method:     http.MethodGet,
			path:       "/api/confirm/token",
			wantStatus: http.StatusBadRequest,
		},
		{
			name:       "unsubscribe success",
			svc:        &contractService{},
			method:     http.MethodGet,
			path:       "/api/unsubscribe/token",
			wantStatus: http.StatusOK,
		},
		{
			name:       "unsubscribe token not found",
			svc:        &contractService{unsubscribeErr: service.ErrTokenNotFound},
			method:     http.MethodGet,
			path:       "/api/unsubscribe/token",
			wantStatus: http.StatusNotFound,
		},
		{
			name:       "unsubscribe invalid token",
			svc:        &contractService{unsubscribeErr: errors.New("invalid token format")},
			method:     http.MethodGet,
			path:       "/api/unsubscribe/token",
			wantStatus: http.StatusBadRequest,
		},
		{
			name:       "subscriptions invalid email",
			svc:        &contractService{},
			method:     http.MethodGet,
			path:       "/api/subscriptions?email=invalid",
			apiKey:     "secret",
			wantStatus: http.StatusBadRequest,
		},
		{
			name: "subscriptions success",
			svc: &contractService{getSubsResult: []*models.Subscription{{Email: "u@example.com", Repo: "owner/repo", Confirmed: true, LastSeenTag: "v1.0.0"}}},
			method:     http.MethodGet,
			path:       "/api/subscriptions?email=u@example.com",
			apiKey:     "secret",
			wantStatus: http.StatusOK,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := newContractRouter(tt.svc, "secret")
			req := httptest.NewRequest(tt.method, tt.path, strings.NewReader(tt.body))
			if tt.contentType != "" {
				req.Header.Set("Content-Type", tt.contentType)
			}
			if tt.apiKey != "" {
				req.Header.Set("X-API-Key", tt.apiKey)
			}
			rr := httptest.NewRecorder()

			r.ServeHTTP(rr, req)

			if rr.Code != tt.wantStatus {
				t.Fatalf("status = %d, want %d (body=%s)", rr.Code, tt.wantStatus, rr.Body.String())
			}

			if tt.wantStatus == http.StatusOK && strings.HasPrefix(tt.path, "/api/subscriptions") {
				var out []map[string]any
				if err := json.Unmarshal(rr.Body.Bytes(), &out); err != nil {
					t.Fatalf("subscriptions response should be JSON array: %v", err)
				}
				if len(out) != 1 {
					t.Fatalf("subscriptions length = %d, want 1", len(out))
				}
			}
		})
	}
}

func TestAuthBoundaries(t *testing.T) {
	r := newContractRouter(&contractService{}, "secret")

	tests := []struct {
		name       string
		method     string
		path       string
		headers    map[string]string
		body       []byte
		wantStatus int
	}{
		{
			name:       "subscribe requires api key",
			method:     http.MethodPost,
			path:       "/api/subscribe",
			headers:    map[string]string{"Content-Type": "application/json"},
			body:       []byte(`{"email":"u@example.com","repo":"owner/repo"}`),
			wantStatus: http.StatusUnauthorized,
		},
		{
			name:       "subscriptions requires api key",
			method:     http.MethodGet,
			path:       "/api/subscriptions?email=u@example.com",
			wantStatus: http.StatusUnauthorized,
		},
		{
			name:       "confirm is public",
			method:     http.MethodGet,
			path:       "/api/confirm/token",
			wantStatus: http.StatusOK,
		},
		{
			name:       "unsubscribe is public",
			method:     http.MethodGet,
			path:       "/api/unsubscribe/token",
			wantStatus: http.StatusOK,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(tt.method, tt.path, bytes.NewReader(tt.body))
			for k, v := range tt.headers {
				req.Header.Set(k, v)
			}
			rr := httptest.NewRecorder()

			r.ServeHTTP(rr, req)

			if rr.Code != tt.wantStatus {
				t.Fatalf("status = %d, want %d", rr.Code, tt.wantStatus)
			}
		})
	}
}
