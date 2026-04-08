package github

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/isprutfromua/ga-test/internal/cache"
	"github.com/isprutfromua/ga-test/internal/config"
	"github.com/isprutfromua/ga-test/internal/models"
)

var (
	ErrInvalidRepo = errors.New("invalid repo format")
	ErrNotFound    = errors.New("not found")
	ErrRateLimited = errors.New("rate limited")
)

var repoPattern = regexp.MustCompile(`^[^/\s]+/[^/\s]+$`)

type Client interface {
	RepoExists(ctx context.Context, repo string) error
	LatestRelease(ctx context.Context, repo string) (*models.GitHubRelease, error)
}

type client struct {
	httpClient *http.Client
	baseURL    string
	token      string
	cache      cache.Cache
	ttl        time.Duration
}

func NewClient(cfg config.GitHubConfig, c cache.Cache, ttl time.Duration) Client {
	return &client{httpClient: &http.Client{Timeout: 10 * time.Second}, baseURL: strings.TrimRight(cfg.Base, "/"), token: cfg.Token, cache: c, ttl: ttl}
}

func ValidateRepoFormat(repo string) error {
	if !repoPattern.MatchString(repo) { return ErrInvalidRepo }
	return nil
}

func (c *client) RepoExists(ctx context.Context, repo string) error {
	if err := ValidateRepoFormat(repo); err != nil { return err }
	cacheKey := "github:repo:" + repo
	var exists bool
	if ok, err := c.cache.Get(ctx, cacheKey, &exists); err == nil && ok {
		if exists { return nil }
		return ErrNotFound
	}
	status, _, err := c.getJSON(ctx, "/repos/"+repo)
	if err != nil { return err }
	if status == http.StatusTooManyRequests || status == http.StatusForbidden {
		return ErrRateLimited
	}
	exists = status >= 200 && status < 300
	_ = c.cache.Set(ctx, cacheKey, exists, c.ttl)
	if !exists { return ErrNotFound }
	return nil
}

func (c *client) LatestRelease(ctx context.Context, repo string) (*models.GitHubRelease, error) {
	if err := ValidateRepoFormat(repo); err != nil { return nil, err }
	cacheKey := "github:release:" + repo
	var release models.GitHubRelease
	if ok, err := c.cache.Get(ctx, cacheKey, &release); err == nil && ok { return &release, nil }
	status, body, err := c.getJSON(ctx, "/repos/"+repo+"/releases/latest")
	if err != nil { return nil, err }
	if status == http.StatusNotFound { return nil, ErrNotFound }
	if status == http.StatusTooManyRequests || (status == http.StatusForbidden && rateLimited(body)) { return nil, ErrRateLimited }
	if status < 200 || status >= 300 { return nil, fmt.Errorf("github latest release: unexpected status %d", status) }
	if err := json.Unmarshal(body, &release); err != nil { return nil, err }
	_ = c.cache.Set(ctx, cacheKey, release, c.ttl)
	return &release, nil
}

func (c *client) getJSON(ctx context.Context, path string) (int, []byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+path, nil)
	if err != nil { return 0, nil, err }
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "github-release-notifier")
	if c.token != "" { req.Header.Set("Authorization", "Bearer "+c.token) }
	res, err := c.httpClient.Do(req)
	if err != nil { return 0, nil, err }
	defer res.Body.Close()
	body, err := io.ReadAll(res.Body)
	if err != nil { return res.StatusCode, nil, err }
	return res.StatusCode, body, nil
}

func rateLimited(body []byte) bool { return strings.Contains(strings.ToLower(string(body)), "rate limit") }
