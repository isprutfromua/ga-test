package service

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"time"

	ghclient "github.com/isprutfromua/ga-test/internal/github"
	"github.com/isprutfromua/ga-test/internal/mailer"
	"github.com/isprutfromua/ga-test/internal/metrics"
	"github.com/isprutfromua/ga-test/internal/models"
	"github.com/isprutfromua/ga-test/internal/repository"
)

const (
	confirmationWorkers   = 4
	confirmationQueueSize = 256
	repoExistsTimeout     = 8 * time.Second
)

var (
	ErrInvalidRepo   = ghclient.ErrInvalidRepo
	ErrRepoNotFound  = ghclient.ErrNotFound
	ErrAlreadyExists = repository.ErrAlreadyExists
	ErrTokenNotFound = repository.ErrNotFound
	ErrRateLimited   = ghclient.ErrRateLimited
)

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
	mailQ   chan confirmationJob
}

func NewSubscriptionService(repo repository.SubscriptionRepository, github ghclient.Client, m mailer.Mailer, met *metrics.Metrics, baseURL string) SubscriptionService {
	s := &subscriptionService{
		repo:    repo,
		github:  github,
		mailer:  m,
		metrics: met,
		baseURL: baseURL,
		mailQ:   make(chan confirmationJob, confirmationQueueSize),
	}

	for i := 0; i < confirmationWorkers; i++ {
		go s.runConfirmationWorker()
	}

	return s
}

type confirmationJob struct {
	email      string
	repo       string
	confirmURL string
}

func (s *subscriptionService) runConfirmationWorker() {
	for job := range s.mailQ {
		if err := s.mailer.SendConfirmation(job.email, job.repo, job.confirmURL); err != nil {
			s.metrics.EmailErrors.Inc()
			continue
		}
		s.metrics.EmailsSent.Inc()
	}
}

func (s *subscriptionService) Subscribe(ctx context.Context, email, repo string) error {
	if err := ghclient.ValidateRepoFormat(repo); err != nil { return ErrInvalidRepo }
	repoCtx, cancel := context.WithTimeout(ctx, repoExistsTimeout)
	defer cancel()
	if err := s.github.RepoExists(repoCtx, repo); err != nil { return err }
	confirmToken, err := generateToken()
	if err != nil { return fmt.Errorf("generating confirm token: %w", err) }
	unsubToken, err := generateToken()
	if err != nil { return fmt.Errorf("generating unsubscribe token: %w", err) }
	sub := &models.Subscription{Email: email, Repo: repo, Confirmed: false, LastSeenTag: "", ConfirmToken: confirmToken, UnsubscribeToken: unsubToken}
	if err := s.repo.Create(ctx, sub); err != nil { return err }
	s.metrics.SubscriptionsTotal.Inc()

	confirmURL := fmt.Sprintf("%s/api/confirm/%s", s.baseURL, confirmToken)
	select {
	case s.mailQ <- confirmationJob{email: email, repo: repo, confirmURL: confirmURL}:
	default:
		// Queue saturation is treated as a delivery failure while preserving API contract.
		s.metrics.EmailErrors.Inc()
	}

	return nil
}

func (s *subscriptionService) Confirm(ctx context.Context, token string) error {
	if !isValidToken(token) { return errors.New("invalid token format") }
	sub, err := s.repo.GetByConfirmToken(ctx, token)
	if err != nil { return ErrTokenNotFound }
	if err := s.repo.Confirm(ctx, sub.ID); err != nil { return fmt.Errorf("confirming subscription: %w", err) }
	s.metrics.ConfirmationsTotal.Inc()
	s.metrics.ActiveSubscriptions.Inc()
	return nil
}

func (s *subscriptionService) Unsubscribe(ctx context.Context, token string) error {
	if !isValidToken(token) { return errors.New("invalid token format") }
	sub, err := s.repo.GetByUnsubscribeToken(ctx, token)
	if err != nil { return ErrTokenNotFound }
	if err := s.repo.Delete(ctx, sub.ID); err != nil { return fmt.Errorf("deleting subscription: %w", err) }
	s.metrics.UnsubscribesTotal.Inc()
	if sub.Confirmed { s.metrics.ActiveSubscriptions.Dec() }
	return nil
}

func (s *subscriptionService) GetSubscriptions(ctx context.Context, email string) ([]*models.Subscription, error) {
	subs, err := s.repo.GetByEmail(ctx, email)
	if err != nil { return nil, fmt.Errorf("fetching subscriptions: %w", err) }
	if subs == nil { subs = []*models.Subscription{} }
	return subs, nil
}

func generateToken() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil { return "", err }
	return hex.EncodeToString(b), nil
}

func isValidToken(token string) bool {
	if len(token) != 64 { return false }
	for _, c := range token {
		if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f')) { return false }
	}
	return true
}
