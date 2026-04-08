package config

import "testing"

func setRequiredBaseEnv(t *testing.T) {
	t.Helper()
	t.Setenv("API_KEY", "test-api-key")
	t.Setenv("DATABASE_URL", "postgres://user:pass@localhost:5432/db?sslmode=disable")
	t.Setenv("SMTP_FROM", "noreply@example.com")
}

func TestLoadCloudMailinSMTPURL(t *testing.T) {
	setRequiredBaseEnv(t)
	t.Setenv("SMTP_HOST", "")
	t.Setenv("CLOUDMAILIN_SMTP_URL", "smtp://usr:pswd@smtp.cloudmailin.net:587?starttls=true")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	if cfg.SMTP.Host != "smtp.cloudmailin.net" {
		t.Fatalf("SMTP.Host = %q, want smtp.cloudmailin.net", cfg.SMTP.Host)
	}
	if cfg.SMTP.Port != "587" {
		t.Fatalf("SMTP.Port = %q, want 587", cfg.SMTP.Port)
	}
	if cfg.SMTP.Username != "usr" {
		t.Fatalf("SMTP.Username = %q, want usr", cfg.SMTP.Username)
	}
	if cfg.SMTP.Password != "pswd" {
		t.Fatalf("SMTP.Password = %q, want pswd", cfg.SMTP.Password)
	}
	if !cfg.SMTP.UseTLS {
		t.Fatal("SMTP.UseTLS = false, want true")
	}
	if cfg.SMTP.From != "noreply@example.com" {
		t.Fatalf("SMTP.From = %q, want noreply@example.com", cfg.SMTP.From)
	}
}

func TestLoadSMTPHostFallback(t *testing.T) {
	setRequiredBaseEnv(t)
	t.Setenv("CLOUDMAILIN_SMTP_URL", "")
	t.Setenv("SMTP_HOST", "smtp.example.com")
	t.Setenv("SMTP_PORT", "2525")
	t.Setenv("SMTP_USERNAME", "user")
	t.Setenv("SMTP_PASSWORD", "pass")
	t.Setenv("SMTP_TLS", "true")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	if cfg.SMTP.Host != "smtp.example.com" {
		t.Fatalf("SMTP.Host = %q, want smtp.example.com", cfg.SMTP.Host)
	}
	if cfg.SMTP.Port != "2525" {
		t.Fatalf("SMTP.Port = %q, want 2525", cfg.SMTP.Port)
	}
	if cfg.SMTP.Username != "user" {
		t.Fatalf("SMTP.Username = %q, want user", cfg.SMTP.Username)
	}
	if cfg.SMTP.Password != "pass" {
		t.Fatalf("SMTP.Password = %q, want pass", cfg.SMTP.Password)
	}
	if !cfg.SMTP.UseTLS {
		t.Fatal("SMTP.UseTLS = false, want true")
	}
}

func TestLoadCloudMailinSMTPURLInvalid(t *testing.T) {
	setRequiredBaseEnv(t)
	t.Setenv("SMTP_HOST", "")
	t.Setenv("CLOUDMAILIN_SMTP_URL", "smtp://:587")

	if _, err := Load(); err == nil {
		t.Fatal("Load() error = nil, want error for invalid CLOUDMAILIN_SMTP_URL")
	}
}
