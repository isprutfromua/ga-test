//go:build ignore

package service

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"

	ghclient "github.com/isprutfromua/ga-test/internal/github"
	"github.com/isprutfromua/ga-test/internal/mailer"
	"github.com/isprutfromua/ga-test/internal/metrics"
	"github.com/isprutfromua/ga-test/internal/models"
	"github.com/isprutfromua/ga-test/internal/repository"
)

// Sentinel errors for the service layer — map cleanly to HTTP status codes.
var (
	ErrInvalidRepo    = ghclient.ErrInvalidRepo
	ErrRepoNotFound   = ghclient.ErrNotFound
	ErrAlreadyExists  = repository.ErrAlreadyExists
	ErrTokenNotFound  = repository.ErrNotFound
	ErrRateLimited    = ghclient.ErrRateLimited
)

// SubscriptionService encapsulates all subscription business logic.
type SubscriptionService interface {
	Subscribe(ctx context.Context, email, repo string) error
	Confirm(ctx context.Context, token string) error
	Unsubscribe(ctx context.Context, token string) error
	GetSubscriptions(ctx context.Context, email string) ([]*models.Subscription, error)
}

type subscriptionService struct {
	repo    repository.SubscriptionRepository
	github  ghclient.Client
	mailer  mailer.Mailer
	metrics *metrics.Metrics
	baseURL string
}

// NewSubscriptionService wires the service with its dependencies.
func NewSubscriptionService(
	repo repository.SubscriptionRepository,
	github ghclient.Client,
	m mailer.Mailer,
	met *metrics.Metrics,
	baseURL string,
) SubscriptionService {
	return &subscriptionService{
		repo:    repo,
		github:  github,
		mailer:  m,
		metrics: met,
		baseURL: baseURL,
	}
}

// Subscribe validates the repo, persists the subscription, and dispatches a
// confirmation email. It is idempotent in the sense that a duplicate request
// returns ErrAlreadyExists so the caller can return HTTP 409.
func (s *subscriptionService) Subscribe(ctx context.Context, email, repo string) error {
	// 1. Validate format before hitting GitHub.
	if err := ghclient.ValidateRepoFormat(repo); err != nil {
		return ErrInvalidRepo
	}

	// 2. Verify repo exists via GitHub API (cached).
	if err := s.github.RepoExists(ctx, repo); err != nil {
		return err // propagate ErrNotFound / ErrRateLimited as-is
	}

	// 3. Generate cryptographically secure tokens.
	confirmToken, err := generateToken()
	if err != nil {
		return fmt.Errorf("generating confirm token: %w", err)
	}
	unsubToken, err := generateToken()
	if err != nil {
		return fmt.Errorf("generating unsubscribe token: %w", err)
	}

	sub := &models.Subscription{
		Email:            email,
		Repo:             repo,
		Confirmed:        false,
		LastSeenTag:      "",
		ConfirmToken:     confirmToken,
		UnsubscribeToken: unsubToken,
	}

	// 4. Persist — returns ErrAlreadyExists if (email, repo) already exists.
	if err := s.repo.Create(ctx, sub); err != nil {
		return err
	}

	s.metrics.SubscriptionsTotal.Inc()

	// 5. Send confirmation email asynchronously to keep the HTTP response fast.
	// We accept the risk that the email may fail (retries are out of scope for v1).
	go func() {
		confirmURL := fmt.Sprintf("%s/api/confirm/%s", s.baseURL, confirmToken)
		if err := s.mailer.SendConfirmation(email, repo, confirmURL); err != nil {
			s.metrics.EmailErrors.Inc()
			// Log via stderr — a production service would use structured logging.
			fmt.Printf("ERROR sending confirmation to %s: %v\n", email, err)
			return
		}
		s.metrics.EmailsSent.Inc()
	}()

	return nil
}

// Confirm activates the subscription identified by the given token.
func (s *subscriptionService) Confirm(ctx context.Context, token string) error {
	if !isValidToken(token) {
		return errors.New("invalid token format")
	}

	sub, err := s.repo.GetByConfirmToken(ctx, token)
	if err != nil {
		return ErrTokenNotFound
	}

	if err := s.repo.Confirm(ctx, sub.ID); err != nil {
		return fmt.Errorf("confirming subscription: %w", err)
	}

	s.metrics.ConfirmationsTotal.Inc()
	s.metrics.ActiveSubscriptions.Inc()
	return nil
}

// Unsubscribe deletes the subscription identified by the given token.
func (s *subscriptionService) Unsubscribe(ctx context.Context, token string) error {
	if !isValidToken(token) {
		return errors.New("invalid token format")
	}

	sub, err := s.repo.GetByUnsubscribeToken(ctx, token)
	if err != nil {
		return ErrTokenNotFound
	}

	if err := s.repo.Delete(ctx, sub.ID); err != nil {
		return fmt.Errorf("deleting subscription: %w", err)
	}

	s.metrics.UnsubscribesTotal.Inc()
	if sub.Confirmed {
		s.metrics.ActiveSubscriptions.Dec()
	}
	return nil
}

// GetSubscriptions returns all subscriptions (confirmed and pending) for an email.
func (s *subscriptionService) GetSubscriptions(ctx context.Context, email string) ([]*models.Subscription, error) {
	subs, err := s.repo.GetByEmail(ctx, email)
	if err != nil {
		return nil, fmt.Errorf("fetching subscriptions: %w", err)
	}
	if subs == nil {
		subs = []*models.Subscription{} // ensure non-nil JSON array
	}
	return subs, nil
}

// ── helpers ───────────────────────────────────────────────────────────────────

// generateToken creates a 32-byte cryptographically random hex token (64 chars).
func generateToken() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

// isValidToken performs a fast sanity check — 64 hex chars.
func isValidToken(token string) bool {
	if len(token) != 64 {
		return false
	}
	for _, c := range token {
		if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f')) {
			return false
		}
	}
	return true
}
