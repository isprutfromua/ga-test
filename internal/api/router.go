package api

import (
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/isprutfromua/ga-test/internal/metrics"
)

func NewRouter(h *Handler, met *metrics.Metrics, apiKey, staticDir string) http.Handler {
	r := chi.NewRouter()
	r.Use(middleware.Recoverer)
	r.Use(middleware.RequestID)
	r.Use(MetricsMiddleware(met))

	r.Get("/healthz", HealthHandler)
	r.Handle("/metrics", MetricsHandler())

	r.Route("/api", func(api chi.Router) {
		api.Group(func(protected chi.Router) {
			protected.Use(APIKeyMiddleware(apiKey))
			protected.Post("/subscribe", h.Subscribe)
			protected.Get("/subscriptions", h.GetSubscriptions)
		})

		api.Get("/confirm/{token}", h.ConfirmSubscription)
		api.Get("/unsubscribe/{token}", h.Unsubscribe)
	})

	r.Handle("/*", http.FileServer(http.Dir(staticDir)))

	return r
}
