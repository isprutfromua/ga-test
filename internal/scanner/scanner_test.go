package scanner

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	ghclient "github.com/isprutfromua/ga-test/internal/github"
	"github.com/isprutfromua/ga-test/internal/mailer"
	"github.com/isprutfromua/ga-test/internal/metrics"
	"github.com/isprutfromua/ga-test/internal/models"
	"github.com/prometheus/client_golang/prometheus"
	io_prometheus_client "github.com/prometheus/client_model/go"
)

type fakeRepo struct {
	getAllConfirmedSubs []*models.Subscription
	getAllConfirmedErr  error
	updateLastSeenErr   error
	acquireLockErr      error
	lockAcquired        bool
	acquireLockCalls    int
	wasNotifiedErr      error
	markNotifiedErr     error
	notificationLog     map[string]bool

	updateCalls []updateCall
}

type updateCall struct {
	id  int64
	tag string
}

func (f *fakeRepo) Create(context.Context, *models.Subscription) error { return nil }
func (f *fakeRepo) GetByConfirmToken(context.Context, string) (*models.Subscription, error) {
	return nil, nil
}
func (f *fakeRepo) GetByUnsubscribeToken(context.Context, string) (*models.Subscription, error) {
	return nil, nil
}
func (f *fakeRepo) Confirm(context.Context, int64) error                       { return nil }
func (f *fakeRepo) Delete(context.Context, int64) error                        { return nil }
func (f *fakeRepo) GetByEmail(context.Context, string) ([]*models.Subscription, error) {
	return nil, nil
}
func (f *fakeRepo) GetAllConfirmed(context.Context) ([]*models.Subscription, error) {
	if f.getAllConfirmedErr != nil {
		return nil, f.getAllConfirmedErr
	}
	return f.getAllConfirmedSubs, nil
}
func (f *fakeRepo) UpdateLastSeenTag(_ context.Context, id int64, tag string) error {
	f.updateCalls = append(f.updateCalls, updateCall{id: id, tag: tag})
	return f.updateLastSeenErr
}
func (f *fakeRepo) AcquireScanLock(context.Context) (func(), bool, error) {
	f.acquireLockCalls++
	if f.acquireLockErr != nil {
		return nil, false, f.acquireLockErr
	}
	return func() {}, f.lockAcquired, nil
}
func (f *fakeRepo) WasNotified(_ context.Context, subscriptionID int64, tag string) (bool, error) {
	if f.wasNotifiedErr != nil {
		return false, f.wasNotifiedErr
	}
	if f.notificationLog == nil {
		return false, nil
	}
	return f.notificationLog[notificationKey(subscriptionID, tag)], nil
}
func (f *fakeRepo) MarkNotified(_ context.Context, subscriptionID int64, tag string) error {
	if f.markNotifiedErr != nil {
		return f.markNotifiedErr
	}
	if f.notificationLog == nil {
		f.notificationLog = map[string]bool{}
	}
	f.notificationLog[notificationKey(subscriptionID, tag)] = true
	return nil
}

type fakeGitHub struct {
	latestRelease    *models.GitHubRelease
	latestReleaseErr error
	latestCalls      int
}

func (f *fakeGitHub) RepoExists(context.Context, string) error { return nil }
func (f *fakeGitHub) LatestRelease(context.Context, string) (*models.GitHubRelease, error) {
	f.latestCalls++
	if f.latestReleaseErr != nil {
		return nil, f.latestReleaseErr
	}
	return f.latestRelease, nil
}

type sentMail struct {
	email    string
	repo     string
	release  mailer.ReleaseInfo
	unsubURL string
}

type fakeMailer struct {
	sendErr error
	sent    []sentMail
}

func (f *fakeMailer) SendConfirmation(string, string, string) error { return nil }
func (f *fakeMailer) SendReleaseNotificationWithUnsub(email, repo string, release mailer.ReleaseInfo, unsubURL string) error {
	f.sent = append(f.sent, sentMail{email: email, repo: repo, release: release, unsubURL: unsubURL})
	return f.sendErr
}

func TestNewDefaultsWorkersToOne(t *testing.T) {
	s := New(&fakeRepo{}, &fakeGitHub{}, &fakeMailer{}, newTestMetrics(), "http://example.com", time.Minute, 0)
	if s.workers != 1 {
		t.Fatalf("workers = %d, want 1", s.workers)
	}
}

func TestScanGetAllConfirmedError(t *testing.T) {
	repo := &fakeRepo{getAllConfirmedErr: errors.New("db down"), lockAcquired: true}
	m := newTestMetrics()
	s := &Scanner{repo: repo, github: &fakeGitHub{}, mailer: &fakeMailer{}, metrics: m, baseURL: "http://example.com", workers: 1}

	s.scan(context.Background())

	if got := counterValue(t, m.ScanErrors); got != 1 {
		t.Fatalf("ScanErrors = %v, want 1", got)
	}
}

func TestScanLockNotAcquiredSkipsCycle(t *testing.T) {
	repo := &fakeRepo{lockAcquired: false, getAllConfirmedSubs: []*models.Subscription{{ID: 1, Repo: "owner/repo"}}}
	m := newTestMetrics()
	s := &Scanner{repo: repo, github: &fakeGitHub{latestRelease: &models.GitHubRelease{TagName: "v1.0.0"}}, mailer: &fakeMailer{}, metrics: m, baseURL: "http://example.com", workers: 1}

	s.scan(context.Background())

	if repo.acquireLockCalls != 1 {
		t.Fatalf("AcquireScanLock calls = %d, want 1", repo.acquireLockCalls)
	}
	if got := counterValue(t, m.ScanErrors); got != 0 {
		t.Fatalf("ScanErrors = %v, want 0", got)
	}
}

func TestScanLockErrorIncrementsScanErrors(t *testing.T) {
	repo := &fakeRepo{acquireLockErr: errors.New("lock unavailable")}
	m := newTestMetrics()
	s := &Scanner{repo: repo, github: &fakeGitHub{}, mailer: &fakeMailer{}, metrics: m, baseURL: "http://example.com", workers: 1}

	s.scan(context.Background())

	if got := counterValue(t, m.ScanErrors); got != 1 {
		t.Fatalf("ScanErrors = %v, want 1", got)
	}
}

func TestCheckRepo(t *testing.T) {
	tests := []struct {
		name                 string
		release              *models.GitHubRelease
		githubErr            error
		mailerErr            error
		updateErr            error
		lastTag              string
		wantEmailsSent       float64
		wantEmailErrors      float64
		wantScanErrors       float64
		wantRateLimitHits    float64
		wantUpdateCalls      int
		wantMailCalls        int
		wantUnsubURLContains string
	}{
		{
			name:            "github not found is ignored",
			githubErr:       ghclient.ErrNotFound,
			wantScanErrors:  0,
			wantMailCalls:   0,
			wantUpdateCalls: 0,
		},
		{
			name:              "github rate limited increments metric",
			githubErr:         ghclient.ErrRateLimited,
			wantRateLimitHits: 1,
			wantMailCalls:     0,
			wantUpdateCalls:   0,
		},
		{
			name:            "github generic error increments scan errors",
			githubErr:       errors.New("network error"),
			wantScanErrors:  1,
			wantMailCalls:   0,
			wantUpdateCalls: 0,
		},
		{
			name:            "nil release is ignored",
			release:         nil,
			wantMailCalls:   0,
			wantUpdateCalls: 0,
		},
		{
			name:            "draft release is ignored",
			release:         &models.GitHubRelease{TagName: "v1.0.0", Draft: true},
			wantMailCalls:   0,
			wantUpdateCalls: 0,
		},
		{
			name:            "prerelease is ignored",
			release:         &models.GitHubRelease{TagName: "v1.0.0", Prerelease: true},
			wantMailCalls:   0,
			wantUpdateCalls: 0,
		},
		{
			name:            "same tag is ignored",
			release:         &models.GitHubRelease{TagName: "v1.0.0"},
			lastTag:         "v1.0.0",
			wantMailCalls:   0,
			wantUpdateCalls: 0,
		},
		{
			name:            "already notified is ignored",
			release:         &models.GitHubRelease{TagName: "v1.1.0"},
			lastTag:         "v1.0.0",
			wantMailCalls:   0,
			wantUpdateCalls: 0,
		},
		{
			name:                 "mailer error increments email errors",
			release:              &models.GitHubRelease{TagName: "v1.1.0", Name: "release"},
			mailerErr:            errors.New("smtp down"),
			wantEmailErrors:      1,
			wantMailCalls:        1,
			wantUpdateCalls:      0,
			wantUnsubURLContains: "/api/unsubscribe/token-123",
		},
		{
			name:                 "update last seen failure increments scan errors",
			release:              &models.GitHubRelease{TagName: "v1.2.0", Name: "release"},
			updateErr:            errors.New("write failed"),
			wantEmailsSent:       1,
			wantScanErrors:       1,
			wantMailCalls:        1,
			wantUpdateCalls:      1,
			wantUnsubURLContains: "/api/unsubscribe/token-123",
		},
		{
			name:                 "success sends email and updates tag",
			release:              &models.GitHubRelease{TagName: "v2.0.0", Name: "release title", Body: "body", HTMLURL: "https://example.com/r"},
			wantEmailsSent:       1,
			wantMailCalls:        1,
			wantUpdateCalls:      1,
			wantUnsubURLContains: "/api/unsubscribe/token-123",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			repo := &fakeRepo{updateLastSeenErr: tt.updateErr}
			if tt.name == "already notified is ignored" {
				repo.notificationLog = map[string]bool{notificationKey(42, "v1.1.0"): true}
			}
			gh := &fakeGitHub{latestRelease: tt.release, latestReleaseErr: tt.githubErr}
			m := &fakeMailer{sendErr: tt.mailerErr}
			met := newTestMetrics()
			s := &Scanner{repo: repo, github: gh, mailer: m, metrics: met, baseURL: "http://example.com", workers: 1}

			s.checkRepo(context.Background(), "owner/repo", []subscriptionWork{{
				id:       42,
				email:    "user@example.com",
				unsubTok: "token-123",
				lastTag:  tt.lastTag,
			}})

			if got := counterValue(t, met.EmailsSent); got != tt.wantEmailsSent {
				t.Fatalf("EmailsSent = %v, want %v", got, tt.wantEmailsSent)
			}
			if got := counterValue(t, met.EmailErrors); got != tt.wantEmailErrors {
				t.Fatalf("EmailErrors = %v, want %v", got, tt.wantEmailErrors)
			}
			if got := counterValue(t, met.ScanErrors); got != tt.wantScanErrors {
				t.Fatalf("ScanErrors = %v, want %v", got, tt.wantScanErrors)
			}
			if got := counterValue(t, met.GitHubRateLimitHits); got != tt.wantRateLimitHits {
				t.Fatalf("GitHubRateLimitHits = %v, want %v", got, tt.wantRateLimitHits)
			}
			if len(repo.updateCalls) != tt.wantUpdateCalls {
				t.Fatalf("UpdateLastSeenTag calls = %d, want %d", len(repo.updateCalls), tt.wantUpdateCalls)
			}
			if len(m.sent) != tt.wantMailCalls {
				t.Fatalf("SendReleaseNotificationWithUnsub calls = %d, want %d", len(m.sent), tt.wantMailCalls)
			}
			if tt.wantMailCalls > 0 {
				if got := m.sent[0].email; got != "user@example.com" {
					t.Fatalf("mail email = %q, want user@example.com", got)
				}
				if got := m.sent[0].repo; got != "owner/repo" {
					t.Fatalf("mail repo = %q, want owner/repo", got)
				}
				if !strings.Contains(m.sent[0].unsubURL, tt.wantUnsubURLContains) {
					t.Fatalf("unsub URL = %q, want contains %q", m.sent[0].unsubURL, tt.wantUnsubURLContains)
				}
			}
		})
	}
}

func TestScanDoesNotDuplicateAfterUpdateFailure(t *testing.T) {
	repo := &fakeRepo{getAllConfirmedSubs: []*models.Subscription{{
		ID:               1,
		Email:            "a@example.com",
		Repo:             "owner/repo",
		UnsubscribeToken: "t1",
		LastSeenTag:      "v1.0.0",
	}}, updateLastSeenErr: errors.New("db down"), lockAcquired: true}

	gh := &fakeGitHub{latestRelease: &models.GitHubRelease{TagName: "v2.0.0", Name: "rel"}}
	m := &fakeMailer{}
	met := newTestMetrics()
	s := &Scanner{repo: repo, github: gh, mailer: m, metrics: met, baseURL: "http://example.com", workers: 1}

	s.scan(context.Background())
	s.scan(context.Background())

	if len(m.sent) != 1 {
		t.Fatalf("emails sent calls = %d, want 1", len(m.sent))
	}
}

func notificationKey(subscriptionID int64, tag string) string {
	return fmt.Sprintf("%d:%s", subscriptionID, tag)
}

func TestScanProcessesSubscriptions(t *testing.T) {
	repo := &fakeRepo{getAllConfirmedSubs: []*models.Subscription{
		{ID: 1, Email: "a@example.com", Repo: "owner/repo", UnsubscribeToken: "t1", LastSeenTag: "v1.0.0"},
		{ID: 2, Email: "b@example.com", Repo: "owner/repo", UnsubscribeToken: "t2", LastSeenTag: "v1.0.0"},
	}, lockAcquired: true}

	gh := &fakeGitHub{latestRelease: &models.GitHubRelease{TagName: "v2.0.0", Name: "rel"}}
	m := &fakeMailer{}
	met := newTestMetrics()
	s := &Scanner{repo: repo, github: gh, mailer: m, metrics: met, baseURL: "http://example.com", workers: 1}

	s.scan(context.Background())

	if len(m.sent) != 2 {
		t.Fatalf("emails sent calls = %d, want 2", len(m.sent))
	}
	if gh.latestCalls != 1 {
		t.Fatalf("LatestRelease calls = %d, want 1", gh.latestCalls)
	}
	if len(repo.updateCalls) != 2 {
		t.Fatalf("update calls = %d, want 2", len(repo.updateCalls))
	}
	if got := counterValue(t, met.EmailsSent); got != 2 {
		t.Fatalf("EmailsSent = %v, want 2", got)
	}
}

func newTestMetrics() *metrics.Metrics {
	return &metrics.Metrics{
		EmailsSent:          prometheus.NewCounter(prometheus.CounterOpts{Name: "test_scanner_emails_sent_total", Help: "test"}),
		EmailErrors:         prometheus.NewCounter(prometheus.CounterOpts{Name: "test_scanner_email_errors_total", Help: "test"}),
		ScanErrors:          prometheus.NewCounter(prometheus.CounterOpts{Name: "test_scanner_scan_errors_total", Help: "test"}),
		GitHubRateLimitHits: prometheus.NewCounter(prometheus.CounterOpts{Name: "test_scanner_github_rate_limit_hits_total", Help: "test"}),
		ScanDuration:        prometheus.NewHistogram(prometheus.HistogramOpts{Name: "test_scanner_scan_duration_seconds", Help: "test"}),
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
