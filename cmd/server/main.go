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
	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("loading config: %w", err)
	}

	database, err := db.Open(cfg.Database)
	if err != nil {
		return fmt.Errorf("connecting to database: %w", err)
	}
	defer database.Close()

	if err := db.Migrate(database, cfg.MigrationsPath); err != nil {
		return fmt.Errorf("running migrations: %w", err)
	}

	redisCache, err := cache.NewRedis(cfg.Redis)
	if err != nil {
		return fmt.Errorf("connecting to redis: %w", err)
	}

	met := metrics.New()
	repo := repository.NewPostgresRepository(database)
	gh := ghclient.NewClient(cfg.GitHub, redisCache, cfg.Redis.TTL)
	ml := mailer.New(cfg.SMTP)
	svc := service.NewSubscriptionService(repo, gh, ml, met, cfg.BaseURL)
	handler := api.NewHandler(svc)
	router := api.NewRouter(handler, met, cfg.Auth.APIKey, cfg.StaticDir)

	server := &http.Server{
		Addr:         ":" + cfg.Server.Port,
		Handler:      router,
		ReadTimeout:  cfg.Server.ReadTimeout,
		WriteTimeout: cfg.Server.WriteTimeout,
		IdleTimeout:  cfg.Server.IdleTimeout,
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go scanner.New(repo, gh, ml, met, cfg.BaseURL, cfg.Scanner.Interval, cfg.Scanner.Workers).Run(ctx)
	go func() {
		log.Printf("server listening on :%s", cfg.Server.Port)
		if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Fatalf("server error: %v", err)
		}
	}()

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	cancel()
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer shutdownCancel()
	return server.Shutdown(shutdownCtx)
}
