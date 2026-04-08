//go:build ignore

package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/isprutfromua/ga-test/internal/service"
)

// Handler holds dependencies for all HTTP handlers.
type Handler struct {
	svc service.SubscriptionService
}

// NewHandler creates a new Handler.
func NewHandler(svc service.SubscriptionService) *Handler {
	return &Handler{svc: svc}
}

// Subscribe godoc
// POST /api/subscribe
// Accepts application/x-www-form-urlencoded or application/json.
func (h *Handler) Subscribe(w http.ResponseWriter, r *http.Request) {
	email, repo, ok := parseSubscribeBody(w, r)
	if !ok {
		return
	}

	if email == "" || repo == "" {
		writeJSON(w, http.StatusBadRequest, errorBody("email and repo are required"))
		return
	}

	if !isValidEmail(email) {
		writeJSON(w, http.StatusBadRequest, errorBody("invalid email address"))
		return
	}

	if err := h.svc.Subscribe(r.Context(), email, repo); err != nil {
		switch {
		case errors.Is(err, service.ErrInvalidRepo):
			writeJSON(w, http.StatusBadRequest, errorBody("invalid repo format, expected owner/repo"))
		case errors.Is(err, service.ErrRepoNotFound):
			writeJSON(w, http.StatusNotFound, errorBody("repository not found on GitHub"))
		case errors.Is(err, service.ErrAlreadyExists):
			writeJSON(w, http.StatusConflict, errorBody("email already subscribed to this repository"))
		case errors.Is(err, service.ErrRateLimited):
			writeJSON(w, http.StatusTooManyRequests, errorBody("GitHub API rate limit exceeded, try again later"))
		default:
			writeJSON(w, http.StatusInternalServerError, errorBody("internal server error"))
		}
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{
		"message": "subscription successful, confirmation email sent",
	})
}

// ConfirmSubscription godoc
// GET /api/confirm/{token}
func (h *Handler) ConfirmSubscription(w http.ResponseWriter, r *http.Request) {
	token := chi.URLParam(r, "token")
	if token == "" {
		writeJSON(w, http.StatusBadRequest, errorBody("token is required"))
		return
	}

	if err := h.svc.Confirm(r.Context(), token); err != nil {
		switch {
		case errors.Is(err, service.ErrTokenNotFound):
			writeJSON(w, http.StatusNotFound, errorBody("token not found"))
		default:
			// Covers invalid token format and other validation errors.
			writeJSON(w, http.StatusBadRequest, errorBody("invalid token"))
		}
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{
		"message": "subscription confirmed successfully",
	})
}

// Unsubscribe godoc
// GET /api/unsubscribe/{token}
func (h *Handler) Unsubscribe(w http.ResponseWriter, r *http.Request) {
	token := chi.URLParam(r, "token")
	if token == "" {
		writeJSON(w, http.StatusBadRequest, errorBody("token is required"))
		return
	}

	if err := h.svc.Unsubscribe(r.Context(), token); err != nil {
		switch {
		case errors.Is(err, service.ErrTokenNotFound):
			writeJSON(w, http.StatusNotFound, errorBody("token not found"))
		default:
			writeJSON(w, http.StatusBadRequest, errorBody("invalid token"))
		}
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{
		"message": "unsubscribed successfully",
	})
}

// GetSubscriptions godoc
// GET /api/subscriptions?email={email}
func (h *Handler) GetSubscriptions(w http.ResponseWriter, r *http.Request) {
	email := r.URL.Query().Get("email")
	if email == "" {
		writeJSON(w, http.StatusBadRequest, errorBody("email query parameter is required"))
		return
	}

	if !isValidEmail(email) {
		writeJSON(w, http.StatusBadRequest, errorBody("invalid email address"))
		return
	}

	subs, err := h.svc.GetSubscriptions(r.Context(), email)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, errorBody("internal server error"))
		return
	}

	// Project to the swagger response shape.
	type subResponse struct {
		Email       string `json:"email"`
		Repo        string `json:"repo"`
		Confirmed   bool   `json:"confirmed"`
		LastSeenTag string `json:"last_seen_tag,omitempty"`
	}

	resp := make([]subResponse, 0, len(subs))
	for _, s := range subs {
		resp = append(resp, subResponse{
			Email:       s.Email,
			Repo:        s.Repo,
			Confirmed:   s.Confirmed,
			LastSeenTag: s.LastSeenTag,
		})
	}

	writeJSON(w, http.StatusOK, resp)
}

// ── helpers ───────────────────────────────────────────────────────────────────

func parseSubscribeBody(w http.ResponseWriter, r *http.Request) (email, repo string, ok bool) {
	ct := r.Header.Get("Content-Type")

	switch {
	case strings.Contains(ct, "application/x-www-form-urlencoded"),
		strings.Contains(ct, "multipart/form-data"):
		if err := r.ParseForm(); err != nil {
			writeJSON(w, http.StatusBadRequest, errorBody("failed to parse form data"))
			return "", "", false
		}
		return r.FormValue("email"), r.FormValue("repo"), true

	case strings.Contains(ct, "application/json"):
		var body struct {
			Email string `json:"email"`
			Repo  string `json:"repo"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			writeJSON(w, http.StatusBadRequest, errorBody("invalid JSON body"))
			return "", "", false
		}
		return body.Email, body.Repo, true

	default:
		// Default to form parsing for compatibility with the swagger spec.
		if err := r.ParseForm(); err != nil {
			writeJSON(w, http.StatusBadRequest, errorBody("failed to parse request body"))
			return "", "", false
		}
		return r.FormValue("email"), r.FormValue("repo"), true
	}
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

func errorBody(msg string) map[string]string {
	return map[string]string{"error": msg}
}

// isValidEmail performs a minimal email validation without regex.
func isValidEmail(email string) bool {
	at := strings.Index(email, "@")
	if at < 1 {
		return false
	}
	domain := email[at+1:]
	return strings.Contains(domain, ".") && len(domain) > 2
}
