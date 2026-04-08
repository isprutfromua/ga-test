package service

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/isprutfromua/ga-test/internal/mailer"
	"github.com/isprutfromua/ga-test/internal/metrics"
	"github.com/isprutfromua/ga-test/internal/models"
	"github.com/isprutfromua/ga-test/internal/repository"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_model/go"
)

type fakeRepo struct {
	createErr           error
	getByConfirmErr     error
	getByUnsubscribeErr error
	confirmErr          error
	deleteErr           error
	getByEmailErr       error

	getByConfirmSub     *models.Subscription
	getByUnsubscribeSub *models.Subscription
	getByEmailSubs      []*models.Subscription

	created      *models.Subscription
	confirmedIDs []int64
	deletedIDs   []int64
	createCalls  int
}

func (f *fakeRepo) Create(_ context.Context, sub *models.Subscription) error {
	f.createCalls++
	f.created = sub
	return f.createErr
}

func (f *fakeRepo) GetByConfirmToken(context.Context, string) (*models.Subscription, error) {
	if f.getByConfirmErr != nil {
		return nil, f.getByConfirmErr
	}
	if f.getByConfirmSub != nil {
		return f.getByConfirmSub, nil
	}
	return &models.Subscription{ID: 1}, nil
}

func (f *fakeRepo) GetByUnsubscribeToken(context.Context, string) (*models.Subscription, error) {
	if f.getByUnsubscribeErr != nil {
		return nil, f.getByUnsubscribeErr
	}
	if f.getByUnsubscribeSub != nil {
		return f.getByUnsubscribeSub, nil
	}
	return &models.Subscription{ID: 1}, nil
}

func (f *fakeRepo) Confirm(_ context.Context, id int64) error {
	f.confirmedIDs = append(f.confirmedIDs, id)
	return f.confirmErr
}

func (f *fakeRepo) Delete(_ context.Context, id int64) error {
	f.deletedIDs = append(f.deletedIDs, id)
	return f.deleteErr
}

func (f *fakeRepo) GetByEmail(context.Context, string) ([]*models.Subscription, error) {
	if f.getByEmailErr != nil {
		return nil, f.getByEmailErr
	}
	return f.getByEmailSubs, nil
}

func (f *fakeRepo) GetAllConfirmed(context.Context) ([]*models.Subscription, error) { return []*models.Subscription{}, nil }
func (f *fakeRepo) UpdateLastSeenTag(context.Context, int64, string) error          { return nil }

type fakeGitHub struct {
	repoExistsErr error
	repoExistsCalls int
}

func (f *fakeGitHub) RepoExists(context.Context, string) error {
	f.repoExistsCalls++
	return f.repoExistsErr
}

func (*fakeGitHub) LatestRelease(context.Context, string) (*models.GitHubRelease, error) { return nil, nil }

type confirmationMail struct {
	email      string
	repo       string
	confirmURL string
}

type fakeMailer struct {
	sendConfirmationErr error
	confirmationCalls   chan confirmationMail
}

func (f *fakeMailer) SendConfirmation(email, repo, confirmURL string) error {
	if f.confirmationCalls != nil {
		f.confirmationCalls <- confirmationMail{email: email, repo: repo, confirmURL: confirmURL}
	}
	return f.sendConfirmationErr
}

func (*fakeMailer) SendReleaseNotificationWithUnsub(string, string, mailer.ReleaseInfo, string) error {
	return nil
}

func TestSubscribe(t *testing.T) {
	tests := []struct {
		name                   string
		email                  string
		repoName               string
		githubErr              error
		createErr              error
		mailerErr              error
		wantErr                error
		wantCreate             bool
		wantRepoExistsCalls    int
		wantSubscriptionsTotal float64
		wantEmailsSent         float64
		wantEmailErrors        float64
	}{
		{
			name:                "invalid repo format",
			email:               "user@example.com",
			repoName:            "invalid",
			wantErr:             ErrInvalidRepo,
			wantCreate:          false,
			wantRepoExistsCalls: 0,
		},
		{
			name:                "github repo exists fails",
			email:               "user@example.com",
			repoName:            "owner/repo",
			githubErr:           ErrRepoNotFound,
			wantErr:             ErrRepoNotFound,
			wantCreate:          false,
			wantRepoExistsCalls: 1,
		},
		{
			name:                "repository create fails",
			email:               "user@example.com",
			repoName:            "owner/repo",
			createErr:           ErrAlreadyExists,
			wantErr:             ErrAlreadyExists,
			wantCreate:          true,
			wantRepoExistsCalls: 1,
		},
		{
			name:                   "success with confirmation email sent",
			email:                  "user@example.com",
			repoName:               "owner/repo",
			wantCreate:             true,
			wantRepoExistsCalls:    1,
			wantSubscriptionsTotal: 1,
			wantEmailsSent:         1,
			wantEmailErrors:        0,
		},
		{
			name:                   "success with confirmation email failure",
			email:                  "user@example.com",
			repoName:               "owner/repo",
			mailerErr:              errors.New("smtp unavailable"),
			wantCreate:             true,
			wantRepoExistsCalls:    1,
			wantSubscriptionsTotal: 1,
			wantEmailsSent:         0,
			wantEmailErrors:        1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			repo := &fakeRepo{createErr: tt.createErr}
			gh := &fakeGitHub{repoExistsErr: tt.githubErr}
			m := &fakeMailer{sendConfirmationErr: tt.mailerErr, confirmationCalls: make(chan confirmationMail, 1)}
			met := newTestMetrics()
			svc := NewSubscriptionService(repo, gh, m, met, "http://example.com")

			err := svc.Subscribe(context.Background(), tt.email, tt.repoName)
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("Subscribe() error = %v, want %v", err, tt.wantErr)
			}

			if gh.repoExistsCalls != tt.wantRepoExistsCalls {
				t.Fatalf("RepoExists calls = %d, want %d", gh.repoExistsCalls, tt.wantRepoExistsCalls)
			}

			if gotCreated := repo.created != nil; gotCreated != tt.wantCreate {
				t.Fatalf("created subscription = %v, want %v", gotCreated, tt.wantCreate)
			}

			if tt.wantCreate && repo.created != nil {
				if repo.created.Email != tt.email {
					t.Fatalf("created email = %q, want %q", repo.created.Email, tt.email)
				}
				if repo.created.Repo != tt.repoName {
					t.Fatalf("created repo = %q, want %q", repo.created.Repo, tt.repoName)
				}
				if repo.created.Confirmed {
					t.Fatal("new subscription must start as unconfirmed")
				}
				if !isValidToken(repo.created.ConfirmToken) {
					t.Fatalf("confirm token is not valid hex token: %q", repo.created.ConfirmToken)
				}
				if !isValidToken(repo.created.UnsubscribeToken) {
					t.Fatalf("unsubscribe token is not valid hex token: %q", repo.created.UnsubscribeToken)
				}
			}

			if tt.wantCreate && tt.createErr == nil {
				select {
				case call := <-m.confirmationCalls:
					if call.email != tt.email {
						t.Fatalf("confirmation email recipient = %q, want %q", call.email, tt.email)
					}
					if call.repo != tt.repoName {
						t.Fatalf("confirmation email repo = %q, want %q", call.repo, tt.repoName)
					}
					if !strings.HasPrefix(call.confirmURL, "http://example.com/api/confirm/") {
						t.Fatalf("confirmation URL = %q, expected base URL prefix", call.confirmURL)
					}
					if token := strings.TrimPrefix(call.confirmURL, "http://example.com/api/confirm/"); !isValidToken(token) {
						t.Fatalf("confirmation URL token is invalid: %q", token)
					}
				case <-time.After(200 * time.Millisecond):
					t.Fatal("timed out waiting for confirmation email")
				}
			}

			if got := counterValue(t, met.SubscriptionsTotal); got != tt.wantSubscriptionsTotal {
				t.Fatalf("SubscriptionsTotal = %v, want %v", got, tt.wantSubscriptionsTotal)
			}

			if tt.wantCreate && tt.createErr == nil {
				waitForCounterValue(t, met.EmailsSent, tt.wantEmailsSent)
				waitForCounterValue(t, met.EmailErrors, tt.wantEmailErrors)
			} else {
				if got := counterValue(t, met.EmailsSent); got != 0 {
					t.Fatalf("EmailsSent = %v, want 0", got)
				}
				if got := counterValue(t, met.EmailErrors); got != 0 {
					t.Fatalf("EmailErrors = %v, want 0", got)
				}
			}
		})
	}
}

func TestConfirm(t *testing.T) {
	tests := []struct {
		name            string
		token           string
		repo            *fakeRepo
		wantErrContains string
		wantErrIs       error
		wantConfirmIDs  []int64
		wantCounter     float64
		wantActiveGauge float64
	}{
		{
			name:            "invalid token format",
			token:           "short",
			repo:            &fakeRepo{},
			wantErrContains: "invalid token format",
			wantCounter:     0,
			wantActiveGauge: 0,
		},
		{
			name:           "token not found",
			token:          strings.Repeat("a", 64),
			repo:           &fakeRepo{getByConfirmErr: repository.ErrNotFound},
			wantErrIs:      ErrTokenNotFound,
			wantCounter:    0,
			wantActiveGauge: 0,
		},
		{
			name:            "confirm update fails",
			token:           strings.Repeat("a", 64),
			repo:            &fakeRepo{getByConfirmSub: &models.Subscription{ID: 42}, confirmErr: errors.New("db down")},
			wantErrContains: "confirming subscription",
			wantConfirmIDs:  []int64{42},
			wantCounter:     0,
			wantActiveGauge: 0,
		},
		{
			name:            "success",
			token:           strings.Repeat("b", 64),
			repo:            &fakeRepo{getByConfirmSub: &models.Subscription{ID: 7}},
			wantConfirmIDs:  []int64{7},
			wantCounter:     1,
			wantActiveGauge: 1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			met := newTestMetrics()
			svc := NewSubscriptionService(tt.repo, &fakeGitHub{}, &fakeMailer{}, met, "http://example.com")

			err := svc.Confirm(context.Background(), tt.token)
			if tt.wantErrIs != nil && !errors.Is(err, tt.wantErrIs) {
				t.Fatalf("Confirm() error = %v, want %v", err, tt.wantErrIs)
			}
			if tt.wantErrContains != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErrContains) {
					t.Fatalf("Confirm() error = %v, want contains %q", err, tt.wantErrContains)
				}
			}
			if tt.wantErrIs == nil && tt.wantErrContains == "" && err != nil {
				t.Fatalf("Confirm() unexpected error: %v", err)
			}

			if len(tt.repo.confirmedIDs) != len(tt.wantConfirmIDs) {
				t.Fatalf("Confirm calls = %v, want %v", tt.repo.confirmedIDs, tt.wantConfirmIDs)
			}
			for i := range tt.wantConfirmIDs {
				if tt.repo.confirmedIDs[i] != tt.wantConfirmIDs[i] {
					t.Fatalf("Confirm call ID at %d = %d, want %d", i, tt.repo.confirmedIDs[i], tt.wantConfirmIDs[i])
				}
			}

			if got := counterValue(t, met.ConfirmationsTotal); got != tt.wantCounter {
				t.Fatalf("ConfirmationsTotal = %v, want %v", got, tt.wantCounter)
			}
			if got := gaugeValue(t, met.ActiveSubscriptions); got != tt.wantActiveGauge {
				t.Fatalf("ActiveSubscriptions = %v, want %v", got, tt.wantActiveGauge)
			}
		})
	}
}

func TestUnsubscribe(t *testing.T) {
	tests := []struct {
		name            string
		token           string
		repo            *fakeRepo
		startActive     float64
		wantErrContains string
		wantErrIs       error
		wantDeleteIDs   []int64
		wantCounter     float64
		wantActiveGauge float64
	}{
		{
			name:            "invalid token format",
			token:           "bad",
			repo:            &fakeRepo{},
			wantErrContains: "invalid token format",
			wantCounter:     0,
			wantActiveGauge: 0,
		},
		{
			name:            "token not found",
			token:           strings.Repeat("a", 64),
			repo:            &fakeRepo{getByUnsubscribeErr: repository.ErrNotFound},
			wantErrIs:       ErrTokenNotFound,
			wantCounter:     0,
			wantActiveGauge: 0,
		},
		{
			name:            "delete fails",
			token:           strings.Repeat("a", 64),
			repo:            &fakeRepo{getByUnsubscribeSub: &models.Subscription{ID: 9, Confirmed: true}, deleteErr: errors.New("db error")},
			startActive:     1,
			wantErrContains: "deleting subscription",
			wantDeleteIDs:   []int64{9},
			wantCounter:     0,
			wantActiveGauge: 1,
		},
		{
			name:            "success confirmed subscription decrements active gauge",
			token:           strings.Repeat("f", 64),
			repo:            &fakeRepo{getByUnsubscribeSub: &models.Subscription{ID: 10, Confirmed: true}},
			startActive:     1,
			wantDeleteIDs:   []int64{10},
			wantCounter:     1,
			wantActiveGauge: 0,
		},
		{
			name:            "success unconfirmed subscription keeps active gauge",
			token:           strings.Repeat("e", 64),
			repo:            &fakeRepo{getByUnsubscribeSub: &models.Subscription{ID: 11, Confirmed: false}},
			startActive:     2,
			wantDeleteIDs:   []int64{11},
			wantCounter:     1,
			wantActiveGauge: 2,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			met := newTestMetrics()
			met.ActiveSubscriptions.Add(tt.startActive)
			svc := NewSubscriptionService(tt.repo, &fakeGitHub{}, &fakeMailer{}, met, "http://example.com")

			err := svc.Unsubscribe(context.Background(), tt.token)
			if tt.wantErrIs != nil && !errors.Is(err, tt.wantErrIs) {
				t.Fatalf("Unsubscribe() error = %v, want %v", err, tt.wantErrIs)
			}
			if tt.wantErrContains != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErrContains) {
					t.Fatalf("Unsubscribe() error = %v, want contains %q", err, tt.wantErrContains)
				}
			}
			if tt.wantErrIs == nil && tt.wantErrContains == "" && err != nil {
				t.Fatalf("Unsubscribe() unexpected error: %v", err)
			}

			if len(tt.repo.deletedIDs) != len(tt.wantDeleteIDs) {
				t.Fatalf("Delete calls = %v, want %v", tt.repo.deletedIDs, tt.wantDeleteIDs)
			}
			for i := range tt.wantDeleteIDs {
				if tt.repo.deletedIDs[i] != tt.wantDeleteIDs[i] {
					t.Fatalf("Delete call ID at %d = %d, want %d", i, tt.repo.deletedIDs[i], tt.wantDeleteIDs[i])
				}
			}

			if got := counterValue(t, met.UnsubscribesTotal); got != tt.wantCounter {
				t.Fatalf("UnsubscribesTotal = %v, want %v", got, tt.wantCounter)
			}
			if got := gaugeValue(t, met.ActiveSubscriptions); got != tt.wantActiveGauge {
				t.Fatalf("ActiveSubscriptions = %v, want %v", got, tt.wantActiveGauge)
			}
		})
	}
}

func TestGetSubscriptions(t *testing.T) {
	tests := []struct {
		name            string
		repo            *fakeRepo
		wantLen         int
		wantErrContains string
	}{
		{
			name:            "repository error is wrapped",
			repo:            &fakeRepo{getByEmailErr: errors.New("connection lost")},
			wantErrContains: "fetching subscriptions",
		},
		{
			name:    "nil slice is converted to empty slice",
			repo:    &fakeRepo{getByEmailSubs: nil},
			wantLen: 0,
		},
		{
			name: "returns subscriptions from repository",
			repo: &fakeRepo{getByEmailSubs: []*models.Subscription{
				{ID: 1, Email: "u@example.com", Repo: "owner/repo-1"},
				{ID: 2, Email: "u@example.com", Repo: "owner/repo-2"},
			}},
			wantLen: 2,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			svc := NewSubscriptionService(tt.repo, &fakeGitHub{}, &fakeMailer{}, newTestMetrics(), "http://example.com")

			subs, err := svc.GetSubscriptions(context.Background(), "u@example.com")
			if tt.wantErrContains != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErrContains) {
					t.Fatalf("GetSubscriptions() error = %v, want contains %q", err, tt.wantErrContains)
				}
				return
			}
			if err != nil {
				t.Fatalf("GetSubscriptions() unexpected error: %v", err)
			}
			if subs == nil {
				t.Fatal("GetSubscriptions() returned nil slice")
			}
			if len(subs) != tt.wantLen {
				t.Fatalf("GetSubscriptions() len = %d, want %d", len(subs), tt.wantLen)
			}
		})
	}
}

func TestGenerateToken(t *testing.T) {
	token, err := generateToken()
	if err != nil {
		t.Fatalf("generateToken() unexpected error: %v", err)
	}
	if !isValidToken(token) {
		t.Fatalf("generateToken() produced invalid token: %q", token)
	}
}

func TestIsValidToken(t *testing.T) {
	tests := []struct {
		name  string
		token string
		want  bool
	}{
		{name: "valid lowercase hex token", token: strings.Repeat("a", 64), want: true},
		{name: "too short", token: strings.Repeat("a", 63), want: false},
		{name: "too long", token: strings.Repeat("a", 65), want: false},
		{name: "contains non-hex letter", token: strings.Repeat("g", 64), want: false},
		{name: "contains uppercase hex", token: strings.Repeat("A", 64), want: false},
		{name: "contains symbol", token: strings.Repeat("f", 63) + "-", want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isValidToken(tt.token); got != tt.want {
				t.Fatalf("isValidToken(%q) = %v, want %v", tt.token, got, tt.want)
			}
		})
	}
}

func newTestMetrics() *metrics.Metrics {
	return &metrics.Metrics{
		SubscriptionsTotal: prometheus.NewCounter(prometheus.CounterOpts{Name: "test_subscriptions_total", Help: "test"}),
		ConfirmationsTotal:  prometheus.NewCounter(prometheus.CounterOpts{Name: "test_confirmations_total", Help: "test"}),
		UnsubscribesTotal:   prometheus.NewCounter(prometheus.CounterOpts{Name: "test_unsubscribes_total", Help: "test"}),
		ActiveSubscriptions: prometheus.NewGauge(prometheus.GaugeOpts{Name: "test_active_subscriptions", Help: "test"}),
		EmailsSent:          prometheus.NewCounter(prometheus.CounterOpts{Name: "test_emails_sent_total", Help: "test"}),
		EmailErrors:         prometheus.NewCounter(prometheus.CounterOpts{Name: "test_email_errors_total", Help: "test"}),
	}
}

func counterValue(t *testing.T, c prometheus.Counter) float64 {
	t.Helper()
	m := &io_prometheus_client.Metric{}
	if err := c.Write(m); err != nil {
		t.Fatalf("counter Write() failed: %v", err)
	}
	if m.Counter == nil {
		t.Fatal("counter metric is nil")
	}
	return m.Counter.GetValue()
}

func gaugeValue(t *testing.T, g prometheus.Gauge) float64 {
	t.Helper()
	m := &io_prometheus_client.Metric{}
	if err := g.Write(m); err != nil {
		t.Fatalf("gauge Write() failed: %v", err)
	}
	if m.Gauge == nil {
		t.Fatal("gauge metric is nil")
	}
	return m.Gauge.GetValue()
}

func waitForCounterValue(t *testing.T, c prometheus.Counter, want float64) {
	t.Helper()
	deadline := time.Now().Add(500 * time.Millisecond)
	for {
		if got := counterValue(t, c); got == want {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("counter did not reach value %v in time; got %v", want, counterValue(t, c))
		}
		time.Sleep(10 * time.Millisecond)
	}
}
