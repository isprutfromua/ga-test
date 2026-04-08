//go:build ignore

package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/isprutfromua/ga-test/internal/api"
	"github.com/isprutfromua/ga-test/internal/cache"
	"github.com/isprutfromua/ga-test/internal/config"
	"github.com/isprutfromua/ga-test/internal/db"
	ghclient "github.com/isprutfromua/ga-test/internal/github"
	"github.com/isprutfromua/ga-test/internal/mailer"
	"github.com/isprutfromua/ga-test/internal/metrics"
	"github.com/isprutfromua/ga-test/internal/repository"
	"github.com/isprutfromua/ga-test/internal/scanner"
	"github.com/isprutfromua/ga-test/internal/service"
)

func main() {
	if err := run(); err != nil {
		log.Fatalf("fatal: %v", err)
	}
}

func run() error {
	// ── Configuration ─────────────────────────────────────────────────────────
	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("loading config: %w", err)
	}

	// ── Database ──────────────────────────────────────────────────────────────
	database, err := db.Open(cfg.Database)
	if err != nil {
		return fmt.Errorf("connecting to database: %w", err)
	}
	defer database.Close()

	migrationsPath := envOrDefault("MIGRATIONS_PATH", "./internal/db/migrations")
	if err := db.Migrate(database, migrationsPath); err != nil {
		return fmt.Errorf("running migrations: %w", err)
	}
	log.Println("database migrations applied")

	// ── Redis ─────────────────────────────────────────────────────────────────
	redisCache, err := cache.NewRedis(cfg.Redis)
	if err != nil {
		return fmt.Errorf("connecting to redis: %w", err)
	}
	log.Println("redis connected")

	// ── Dependency graph ──────────────────────────────────────────────────────
	met := metrics.New()
	repo := repository.NewPostgresRepository(database)
	ghClient := ghclient.NewClient(cfg.GitHub, redisCache, cfg.Redis.TTL)
	ml := mailer.New(cfg.SMTP)

	svc := service.NewSubscriptionService(repo, ghClient, ml, met, cfg.BaseURL)
	handler := api.NewHandler(svc)

	staticDir := envOrDefault("STATIC_DIR", "./static")
	router := api.NewRouter(handler, met, cfg.Auth.APIKey, staticDir)

	// ── HTTP server ───────────────────────────────────────────────────────────
	srv := &http.Server{
		Addr:         ":" + cfg.Server.Port,
		Handler:      router,
		ReadTimeout:  cfg.Server.ReadTimeout,
		WriteTimeout: cfg.Server.WriteTimeout,
		IdleTimeout:  cfg.Server.IdleTimeout,
	}

	// ── Background scanner ────────────────────────────────────────────────────
	sc := scanner.New(
		repo, ghClient, ml, met,
		cfg.BaseURL,
		cfg.Scanner.Interval,
		cfg.Scanner.Workers,
	)

	// ── Graceful shutdown ─────────────────────────────────────────────────────
	// scanCtx is cancelled first so the scanner finishes its current cycle
	// before the HTTP server stops accepting new connections.
	scanCtx, cancelScan := context.WithCancel(context.Background())
	defer cancelScan()

	go sc.Run(scanCtx)

	go func() {
		log.Printf("server listening on :%s", cfg.Server.Port)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Fatalf("server error: %v", err)
		}
	}()

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	log.Println("shutdown signal received")

	// Stop scanner first.
	cancelScan()

	// Give in-flight HTTP requests up to 30s to complete.
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	if err := srv.Shutdown(shutdownCtx); err != nil {
		return fmt.Errorf("graceful shutdown failed: %w", err)
	}

	log.Println("server stopped cleanly")
	return nil
}

func envOrDefault(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
