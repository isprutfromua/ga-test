package scanner

import (
	"context"
	"errors"
	"fmt"
	"log"
	"sync"
	"time"

	ghclient "github.com/isprutfromua/ga-test/internal/github"
	"github.com/isprutfromua/ga-test/internal/mailer"
	"github.com/isprutfromua/ga-test/internal/metrics"
	"github.com/isprutfromua/ga-test/internal/repository"
)

type Scanner struct {
	repo     repository.SubscriptionRepository
	github   ghclient.Client
	mailer   mailer.Mailer
	metrics  *metrics.Metrics
	baseURL  string
	interval time.Duration
	workers  int
}

func New(repo repository.SubscriptionRepository, gh ghclient.Client, m mailer.Mailer, met *metrics.Metrics, baseURL string, interval time.Duration, workers int) *Scanner {
	if workers < 1 { workers = 1 }
	return &Scanner{repo: repo, github: gh, mailer: m, metrics: met, baseURL: baseURL, interval: interval, workers: workers}
}

func (s *Scanner) Run(ctx context.Context) {
	ticker := time.NewTicker(s.interval)
	defer ticker.Stop()
	s.scan(ctx)
	for {
		select {
		case <-ticker.C:
			s.scan(ctx)
		case <-ctx.Done():
			return
		}
	}
}

func (s *Scanner) scan(ctx context.Context) {
	start := time.Now()
	defer func() { s.metrics.ScanDuration.Observe(time.Since(start).Seconds()) }()
	subs, err := s.repo.GetAllConfirmed(ctx)
	if err != nil {
		log.Printf("scanner: failed to fetch subscriptions: %v", err)
		s.metrics.ScanErrors.Inc()
		return
	}
	if len(subs) == 0 { return }
	type work struct { id int64; email, repo, unsubTok, lastTag string }
	jobs := make(chan work)
	var wg sync.WaitGroup
	for i := 0; i < s.workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for job := range jobs { s.checkRepo(ctx, job.id, job.email, job.repo, job.unsubTok, job.lastTag) }
		}()
	}
	for _, sub := range subs {
		select {
		case jobs <- work{id: sub.ID, email: sub.Email, repo: sub.Repo, unsubTok: sub.UnsubscribeToken, lastTag: sub.LastSeenTag}:
		case <-ctx.Done():
			close(jobs)
			wg.Wait()
			return
		}
	}
	close(jobs)
	wg.Wait()
}

func (s *Scanner) checkRepo(ctx context.Context, subID int64, email, repo, unsubToken, lastTag string) {
	release, err := s.github.LatestRelease(ctx, repo)
	if err != nil {
		if errors.Is(err, ghclient.ErrNotFound) { return }
		if errors.Is(err, ghclient.ErrRateLimited) { s.metrics.GitHubRateLimitHits.Inc(); return }
		s.metrics.ScanErrors.Inc()
		return
	}
	if release == nil || release.Draft || release.Prerelease || release.TagName == lastTag { return }
	unsubURL := fmt.Sprintf("%s/api/unsubscribe/%s", s.baseURL, unsubToken)
	if err := s.mailer.SendReleaseNotificationWithUnsub(email, repo, mailer.ReleaseInfo{TagName: release.TagName, Name: release.Name, Body: release.Body, HTMLURL: release.HTMLURL, PublishedAt: release.PublishedAt}, unsubURL); err != nil {
		s.metrics.EmailErrors.Inc()
		return
	}
	s.metrics.EmailsSent.Inc()
	if err := s.repo.UpdateLastSeenTag(ctx, subID, release.TagName); err != nil { s.metrics.ScanErrors.Inc() }
}
