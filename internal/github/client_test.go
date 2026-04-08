package github

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/isprutfromua/ga-test/internal/config"
	"github.com/isprutfromua/ga-test/internal/models"
)

type fakeCache struct {
	mu     sync.Mutex
	store  map[string][]byte
	getErr error
	setErr error
}

func newFakeCache() *fakeCache {
	return &fakeCache{store: map[string][]byte{}}
}

func (c *fakeCache) Get(_ context.Context, key string, target any) (bool, error) {
	if c.getErr != nil {
		return false, c.getErr
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	b, ok := c.store[key]
	if !ok {
		return false, nil
	}
	return true, json.Unmarshal(b, target)
}

func (c *fakeCache) Set(_ context.Context, key string, value any, _ time.Duration) error {
	if c.setErr != nil {
		return c.setErr
	}
	b, err := json.Marshal(value)
	if err != nil {
		return err
	}
	c.mu.Lock()
	c.store[key] = b
	c.mu.Unlock()
	return nil
}

func (c *fakeCache) Delete(_ context.Context, key string) error {
	c.mu.Lock()
	delete(c.store, key)
	c.mu.Unlock()
	return nil
}

func TestValidateRepoFormat(t *testing.T) {
	tests := []struct {
		name string
		repo string
		want error
	}{
		{name: "valid", repo: "owner/repo", want: nil},
		{name: "missing slash", repo: "owner", want: ErrInvalidRepo},
		{name: "empty owner", repo: "/repo", want: ErrInvalidRepo},
		{name: "contains spaces", repo: "owner /repo", want: ErrInvalidRepo},
		{name: "extra slash", repo: "o/r/e", want: ErrInvalidRepo},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateRepoFormat(tt.repo)
			if !errors.Is(err, tt.want) {
				t.Fatalf("ValidateRepoFormat(%q) error = %v, want %v", tt.repo, err, tt.want)
			}
		})
	}
}

func TestRepoExists(t *testing.T) {
	t.Run("cache hit true skips network", func(t *testing.T) {
		cache := newFakeCache()
		_ = cache.Set(context.Background(), "github:repo:owner/repo", true, time.Minute)

		reqCount := 0
		ts := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
			reqCount++
		}))
		defer ts.Close()

		c := NewClient(config.GitHubConfig{Base: ts.URL}, cache, time.Minute)
		if err := c.RepoExists(context.Background(), "owner/repo"); err != nil {
			t.Fatalf("RepoExists() unexpected error: %v", err)
		}
		if reqCount != 0 {
			t.Fatalf("network requests = %d, want 0", reqCount)
		}
	})

	t.Run("cache hit false returns not found", func(t *testing.T) {
		cache := newFakeCache()
		_ = cache.Set(context.Background(), "github:repo:owner/repo", false, time.Minute)

		c := NewClient(config.GitHubConfig{Base: "http://example.invalid"}, cache, time.Minute)
		err := c.RepoExists(context.Background(), "owner/repo")
		if !errors.Is(err, ErrNotFound) {
			t.Fatalf("RepoExists() error = %v, want %v", err, ErrNotFound)
		}
	})

	t.Run("invalid repo rejected before network", func(t *testing.T) {
		cache := newFakeCache()
		c := NewClient(config.GitHubConfig{Base: "http://example.invalid"}, cache, time.Minute)
		err := c.RepoExists(context.Background(), "invalid")
		if !errors.Is(err, ErrInvalidRepo) {
			t.Fatalf("RepoExists() error = %v, want %v", err, ErrInvalidRepo)
		}
	})

	tests := []struct {
		name       string
		statusCode int
		body       string
		wantErr    error
		wantCached bool
	}{
		{name: "exists", statusCode: http.StatusOK, body: `{}`, wantErr: nil, wantCached: true},
		{name: "not found", statusCode: http.StatusNotFound, body: `{}`, wantErr: ErrNotFound, wantCached: false},
		{name: "rate limited 429", statusCode: http.StatusTooManyRequests, body: `{}`, wantErr: ErrRateLimited},
		{name: "rate limited 403", statusCode: http.StatusForbidden, body: `{}`, wantErr: ErrRateLimited},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cache := newFakeCache()
			ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/repos/owner/repo" {
					t.Fatalf("path = %q, want /repos/owner/repo", r.URL.Path)
				}
				w.WriteHeader(tt.statusCode)
				_, _ = w.Write([]byte(tt.body))
			}))
			defer ts.Close()

			c := NewClient(config.GitHubConfig{Base: ts.URL}, cache, time.Minute)
			err := c.RepoExists(context.Background(), "owner/repo")
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("RepoExists() error = %v, want %v", err, tt.wantErr)
			}

			if tt.statusCode != http.StatusTooManyRequests && tt.statusCode != http.StatusForbidden {
				var cached bool
				ok, getErr := cache.Get(context.Background(), "github:repo:owner/repo", &cached)
				if getErr != nil || !ok {
					t.Fatalf("expected cache entry, ok=%v err=%v", ok, getErr)
				}
				if cached != tt.wantCached {
					t.Fatalf("cached exists = %v, want %v", cached, tt.wantCached)
				}
			}
		})
	}
}

func TestLatestRelease(t *testing.T) {
	t.Run("cache hit returns cached release", func(t *testing.T) {
		cache := newFakeCache()
		cached := models.GitHubRelease{TagName: "v1.2.3", Name: "R1"}
		_ = cache.Set(context.Background(), "github:release:owner/repo", cached, time.Minute)

		c := NewClient(config.GitHubConfig{Base: "http://example.invalid"}, cache, time.Minute)
		rel, err := c.LatestRelease(context.Background(), "owner/repo")
		if err != nil {
			t.Fatalf("LatestRelease() unexpected error: %v", err)
		}
		if rel == nil || rel.TagName != "v1.2.3" {
			t.Fatalf("LatestRelease() = %#v, want cached release", rel)
		}
	})

	tests := []struct {
		name       string
		statusCode int
		body       string
		wantErr    error
		wantTag    string
	}{
		{name: "not found", statusCode: http.StatusNotFound, body: `{}`, wantErr: ErrNotFound},
		{name: "rate limited 429", statusCode: http.StatusTooManyRequests, body: `{"message":"rate limit"}`, wantErr: ErrRateLimited},
		{name: "rate limited 403 with body", statusCode: http.StatusForbidden, body: `{"message":"API rate limit exceeded"}`, wantErr: ErrRateLimited},
		{name: "success", statusCode: http.StatusOK, body: `{"tag_name":"v2.0.0","name":"v2"}`, wantTag: "v2.0.0"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cache := newFakeCache()
			ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/repos/owner/repo/releases/latest" {
					t.Fatalf("path = %q, want /repos/owner/repo/releases/latest", r.URL.Path)
				}
				w.WriteHeader(tt.statusCode)
				_, _ = w.Write([]byte(tt.body))
			}))
			defer ts.Close()

			c := NewClient(config.GitHubConfig{Base: ts.URL}, cache, time.Minute)
			rel, err := c.LatestRelease(context.Background(), "owner/repo")
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("LatestRelease() error = %v, want %v", err, tt.wantErr)
			}
			if tt.wantTag != "" {
				if rel == nil || rel.TagName != tt.wantTag {
					t.Fatalf("LatestRelease() tag = %v, want %q", rel, tt.wantTag)
				}
			}
		})
	}

	t.Run("invalid JSON returns error", func(t *testing.T) {
		cache := newFakeCache()
		ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"tag_name":`))
		}))
		defer ts.Close()

		c := NewClient(config.GitHubConfig{Base: ts.URL}, cache, time.Minute)
		_, err := c.LatestRelease(context.Background(), "owner/repo")
		if err == nil {
			t.Fatal("LatestRelease() expected JSON error")
		}
	})

	t.Run("unexpected status returns detailed error", func(t *testing.T) {
		cache := newFakeCache()
		ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte(`{"message":"boom"}`))
		}))
		defer ts.Close()

		c := NewClient(config.GitHubConfig{Base: ts.URL}, cache, time.Minute)
		_, err := c.LatestRelease(context.Background(), "owner/repo")
		if err == nil || err.Error() != "github latest release: unexpected status 500" {
			t.Fatalf("LatestRelease() error = %v, want unexpected status error", err)
		}
	})
}

func TestRepoExistsTransportError(t *testing.T) {
	cache := newFakeCache()
	c := NewClient(config.GitHubConfig{Base: "http://127.0.0.1:1"}, cache, time.Minute)
	err := c.RepoExists(context.Background(), "owner/repo")
	if err == nil {
		t.Fatal("RepoExists() expected transport error")
	}
}

func TestRateLimited(t *testing.T) {
	tests := []struct {
		body string
		want bool
	}{
		{body: "API rate limit exceeded", want: true},
		{body: "something else", want: false},
	}

	for _, tt := range tests {
		t.Run(fmt.Sprintf("body_%v", tt.want), func(t *testing.T) {
			if got := rateLimited([]byte(tt.body)); got != tt.want {
				t.Fatalf("rateLimited(%q) = %v, want %v", tt.body, got, tt.want)
			}
		})
	}
}
