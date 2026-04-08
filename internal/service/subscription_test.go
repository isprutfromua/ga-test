package service

import (
	"context"
	"testing"

	ghclient "github.com/isprutfromua/ga-test/internal/github"
	"github.com/isprutfromua/ga-test/internal/mailer"
	"github.com/isprutfromua/ga-test/internal/metrics"
	"github.com/isprutfromua/ga-test/internal/models"
	"github.com/isprutfromua/ga-test/internal/repository"
)

type fakeRepo struct{ created *models.Subscription }

func (f *fakeRepo) Create(ctx context.Context, sub *models.Subscription) error { f.created = sub; return nil }
func (f *fakeRepo) GetByConfirmToken(context.Context, string) (*models.Subscription, error) { return nil, repository.ErrNotFound }
func (f *fakeRepo) GetByUnsubscribeToken(context.Context, string) (*models.Subscription, error) { return nil, repository.ErrNotFound }
func (f *fakeRepo) Confirm(context.Context, int64) error { return nil }
func (f *fakeRepo) Delete(context.Context, int64) error { return nil }
func (f *fakeRepo) GetByEmail(context.Context, string) ([]*models.Subscription, error) { return []*models.Subscription{}, nil }
func (f *fakeRepo) GetAllConfirmed(context.Context) ([]*models.Subscription, error) { return []*models.Subscription{}, nil }
func (f *fakeRepo) UpdateLastSeenTag(context.Context, int64, string) error { return nil }

type fakeGitHub struct{}

func (fakeGitHub) RepoExists(context.Context, string) error { return nil }
func (fakeGitHub) LatestRelease(context.Context, string) (*models.GitHubRelease, error) { return nil, nil }

type fakeMailer struct{}

func (fakeMailer) SendConfirmation(string, string, string) error { return nil }
func (fakeMailer) SendReleaseNotificationWithUnsub(string, string, mailer.ReleaseInfo, string) error { return nil }

func TestSubscribeGeneratesTokens(t *testing.T) {
	repo := &fakeRepo{}
	svc := NewSubscriptionService(repo, fakeGitHub{}, fakeMailer{}, metrics.New(), "http://example.com")
	if err := svc.Subscribe(context.Background(), "user@example.com", "golang/go"); err != nil {
		t.Fatalf("Subscribe returned error: %v", err)
	}
	if repo.created == nil { t.Fatal("expected subscription to be created") }
	if len(repo.created.ConfirmToken) != 64 || len(repo.created.UnsubscribeToken) != 64 {
		t.Fatalf("expected 64-char tokens, got %d and %d", len(repo.created.ConfirmToken), len(repo.created.UnsubscribeToken))
	}
}
