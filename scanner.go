//go:build ignore

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

// Scanner periodically checks all confirmed subscriptions for new releases.
type Scanner struct {
	repo    repository.SubscriptionRepository
	github  ghclient.Client
	mailer  mailer.Mailer
	metrics *metrics.Metrics
	baseURL string
	interval time.Duration
	workers  int
}

// New creates a new Scanner.
func New(
	repo repository.SubscriptionRepository,
	gh ghclient.Client,
	m mailer.Mailer,
	met *metrics.Metrics,
	baseURL string,
	interval time.Duration,
	workers int,
) *Scanner {
	return &Scanner{
		repo:     repo,
		github:   gh,
		mailer:   m,
		metrics:  met,
		baseURL:  baseURL,
		interval: interval,
		workers:  workers,
	}
}

// Run starts the scanning loop and blocks until ctx is cancelled.
func (s *Scanner) Run(ctx context.Context) {
	log.Printf("scanner: starting with interval=%s workers=%d", s.interval, s.workers)

	ticker := time.NewTicker(s.interval)
	defer ticker.Stop()

	// Run once immediately on startup.
	s.scan(ctx)

	for {
		select {
		case <-ticker.C:
			s.scan(ctx)
		case <-ctx.Done():
			log.Println("scanner: stopping")
			return
		}
	}
}

// scan fetches all confirmed subscriptions and checks each repo concurrently
// using a bounded worker pool to avoid exhausting the GitHub rate limit.
func (s *Scanner) scan(ctx context.Context) {
	start := time.Now()
	defer func() {
		s.metrics.ScanDuration.Observe(time.Since(start).Seconds())
	}()

	subs, err := s.repo.GetAllConfirmed(ctx)
	if err != nil {
		log.Printf("scanner: failed to fetch subscriptions: %v", err)
		s.metrics.ScanErrors.Inc()
		return
	}

	if len(subs) == 0 {
		return
	}

	log.Printf("scanner: checking %d subscription(s)", len(subs))

	// Bounded worker pool — prevents goroutine explosion and controls rate-limit pressure.
	type work struct {
		subID    int64
		email    string
		repo     string
		unsubTok string
		lastTag  string
	}

	jobs := make(chan work, len(subs))
	var wg sync.WaitGroup

	for range s.workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := range jobs {
				s.checkRepo(ctx, j.subID, j.email, j.repo, j.unsubTok, j.lastTag)
			}
		}()
	}

	for _, sub := range subs {
		jobs <- work{
			subID:    sub.ID,
			email:    sub.Email,
			repo:     sub.Repo,
			unsubTok: sub.UnsubscribeToken,
			lastTag:  sub.LastSeenTag,
		}
	}
	close(jobs)
	wg.Wait()
}

func (s *Scanner) checkRepo(ctx context.Context, subID int64, email, repo, unsubToken, lastTag string) {
	release, err := s.github.LatestRelease(ctx, repo)
	if err != nil {
		if errors.Is(err, ghclient.ErrNotFound) {
			// No releases yet — nothing to do.
			return
		}
		if errors.Is(err, ghclient.ErrRateLimited) {
			log.Printf("scanner: rate limited while checking %s — backing off", repo)
			s.metrics.GitHubRateLimitHits.Inc()
			return
		}
		log.Printf("scanner: error fetching release for %s: %v", repo, err)
		s.metrics.ScanErrors.Inc()
		return
	}

	// Skip drafts and pre-releases for notifications.
	if release.Draft || release.Prerelease {
		return
	}

	// No new release.
	if release.TagName == lastTag {
		return
	}

	log.Printf("scanner: new release %s for %s — notifying %s", release.TagName, repo, email)

	unsubURL := fmt.Sprintf("%s/api/unsubscribe/%s", s.baseURL, unsubToken)
	err = mailer.SendReleaseNotificationWithUnsub(s.mailer, email, repo, mailer.ReleaseInfo{
		TagName:     release.TagName,
		Name:        release.Name,
		Body:        release.Body,
		HTMLURL:     release.HTMLURL,
		PublishedAt: release.PublishedAt,
	}, unsubURL)

	if err != nil {
		log.Printf("scanner: failed to send notification to %s: %v", email, err)
		s.metrics.EmailErrors.Inc()
		return
	}

	s.metrics.EmailsSent.Inc()

	// Persist the new last_seen_tag so we don't re-notify.
	if err := s.repo.UpdateLastSeenTag(ctx, subID, release.TagName); err != nil {
		log.Printf("scanner: failed to update last_seen_tag for sub %d: %v", subID, err)
		s.metrics.ScanErrors.Inc()
	}
}
