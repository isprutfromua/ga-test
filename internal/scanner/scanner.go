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

const (
	scannerGitHubTimeout = 10 * time.Second
	scannerDBTimeout     = 5 * time.Second
)

type subscriptionWork struct {
	id       int64
	email    string
	unsubTok string
	lastTag  string
}

type repoWork struct {
	repo string
	subs []subscriptionWork
}

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
	release, acquired, err := s.repo.AcquireScanLock(ctx)
	if err != nil {
		log.Printf("scanner: failed to acquire scan lock: %v", err)
		s.metrics.ScanErrors.Inc()
		return
	}
	if !acquired {
		return
	}
	defer release()

	subs, err := s.repo.GetAllConfirmed(ctx)
	if err != nil {
		log.Printf("scanner: failed to fetch subscriptions: %v", err)
		s.metrics.ScanErrors.Inc()
		return
	}
	if len(subs) == 0 { return }

	repoSubs := make(map[string][]subscriptionWork)
	for _, sub := range subs {
		repoSubs[sub.Repo] = append(repoSubs[sub.Repo], subscriptionWork{
			id:       sub.ID,
			email:    sub.Email,
			unsubTok: sub.UnsubscribeToken,
			lastTag:  sub.LastSeenTag,
		})
	}

	jobs := make(chan repoWork)
	var wg sync.WaitGroup
	for i := 0; i < s.workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-ctx.Done():
					return
				case job, ok := <-jobs:
					if !ok {
						return
					}
					s.checkRepo(ctx, job.repo, job.subs)
				}
			}
		}()
	}
	for repo, repoSubscribers := range repoSubs {
		select {
		case jobs <- repoWork{repo: repo, subs: repoSubscribers}:
		case <-ctx.Done():
			close(jobs)
			wg.Wait()
			return
		}
	}
	close(jobs)
	wg.Wait()
}

func (s *Scanner) checkRepo(ctx context.Context, repo string, subs []subscriptionWork) {
	if ctx.Err() != nil {
		return
	}

	ghCtx, cancelGH := context.WithTimeout(ctx, scannerGitHubTimeout)
	release, err := s.github.LatestRelease(ghCtx, repo)
	cancelGH()
	if err != nil {
		if errors.Is(err, ghclient.ErrNotFound) { return }
		if errors.Is(err, ghclient.ErrRateLimited) { s.metrics.GitHubRateLimitHits.Inc(); return }
		s.metrics.ScanErrors.Inc()
		return
	}
	if release == nil || release.Draft || release.Prerelease { return }

	for _, sub := range subs {
		if release.TagName == sub.lastTag {
			continue
		}
		notified, err := s.repo.WasNotified(ctx, sub.id, release.TagName)
		if err != nil {
			s.metrics.ScanErrors.Inc()
			continue
		}
		if notified {
			continue
		}
		if ctx.Err() != nil {
			return
		}
		unsubURL := fmt.Sprintf("%s/api/unsubscribe/%s", s.baseURL, sub.unsubTok)
		if err := s.mailer.SendReleaseNotificationWithUnsub(sub.email, repo, mailer.ReleaseInfo{TagName: release.TagName, Name: release.Name, Body: release.Body, HTMLURL: release.HTMLURL, PublishedAt: release.PublishedAt}, unsubURL); err != nil {
			s.metrics.EmailErrors.Inc()
			continue
		}
		s.metrics.EmailsSent.Inc()
		dbCtxMark, cancelMark := context.WithTimeout(ctx, scannerDBTimeout)
		err = s.repo.MarkNotified(dbCtxMark, sub.id, release.TagName)
		cancelMark()
		if err != nil {
			s.metrics.ScanErrors.Inc()
			continue
		}
		if ctx.Err() != nil {
			return
		}
		dbCtx, cancelDB := context.WithTimeout(ctx, scannerDBTimeout)
		err = s.repo.UpdateLastSeenTag(dbCtx, sub.id, release.TagName)
		cancelDB()
		if err != nil && !errors.Is(err, repository.ErrNotFound) {
			s.metrics.ScanErrors.Inc()
		}
	}
}
