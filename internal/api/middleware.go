package api

import (
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/isprutfromua/ga-test/internal/metrics"
)

func APIKeyMiddleware(expected string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if expected == "" {
				next.ServeHTTP(w, r)
				return
			}
			if r.Header.Get("X-API-Key") != expected {
				writeJSON(w, http.StatusUnauthorized, errorBody("unauthorized"))
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

func MetricsMiddleware(m *metrics.Metrics) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
			start := time.Now()
			next.ServeHTTP(rec, r)
			route := r.URL.Path
			if routeCtx := chi.RouteContext(r.Context()); routeCtx != nil {
				if pattern := routeCtx.RoutePattern(); pattern != "" {
					route = pattern
				}
			}
			m.HTTPRequestDuration.WithLabelValues(r.Method, route, http.StatusText(rec.status)).Observe(time.Since(start).Seconds())
		})
	}
}

type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (r *statusRecorder) WriteHeader(status int) { r.status = status; r.ResponseWriter.WriteHeader(status) }

func HealthHandler(w http.ResponseWriter, _ *http.Request) { writeJSON(w, http.StatusOK, map[string]string{"status": "ok"}) }
func MetricsHandler() http.Handler { return promhttp.Handler() }
