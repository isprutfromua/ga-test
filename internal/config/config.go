package config

import (
	"fmt"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"
)

type Config struct {
	BaseURL        string
	StaticDir      string
	MigrationsPath string
	Server         ServerConfig
	Database       DatabaseConfig
	Redis          RedisConfig
	GitHub         GitHubConfig
	SMTP           SMTPConfig
	Scanner        ScannerConfig
	Auth           AuthConfig
}

type ServerConfig struct {
	Port         string
	ReadTimeout  time.Duration
	WriteTimeout time.Duration
	IdleTimeout  time.Duration
}

type DatabaseConfig struct{ URL string }

type RedisConfig struct {
	Addr     string
	Password string
	DB       int
	UseTLS   bool
	TLSServerName string
	TLSInsecureSkipVerify bool
	TTL      time.Duration
}

type GitHubConfig struct {
	Token string
	Base  string
}

type SMTPConfig struct {
	Host     string
	Port     string
	Username string
	Password string
	From     string
	UseTLS   bool
}

type ScannerConfig struct {
	Interval time.Duration
	Workers  int
}

type AuthConfig struct{ APIKey string }

func Load() (*Config, error) {
	interval, err := durationEnv("SCANNER_INTERVAL", 5*time.Minute)
	if err != nil { return nil, err }
	ttl, err := durationEnv("GITHUB_CACHE_TTL", 10*time.Minute)
	if err != nil { return nil, err }
	readTimeout, err := durationEnv("HTTP_READ_TIMEOUT", 10*time.Second)
	if err != nil { return nil, err }
	writeTimeout, err := durationEnv("HTTP_WRITE_TIMEOUT", 10*time.Second)
	if err != nil { return nil, err }
	idleTimeout, err := durationEnv("HTTP_IDLE_TIMEOUT", 60*time.Second)
	if err != nil { return nil, err }
	apiKey := os.Getenv("API_KEY")
	if apiKey == "" { return nil, fmt.Errorf("API_KEY is required") }
	databaseURL := os.Getenv("DATABASE_URL")
	if databaseURL == "" { return nil, fmt.Errorf("DATABASE_URL is required") }
	smtpCfg, err := loadSMTPConfig()
	if err != nil { return nil, err }
	redisDB, err := intEnv("REDIS_DB", 0)
	if err != nil { return nil, err }
	workers, err := intEnv("SCANNER_WORKERS", 5)
	if err != nil { return nil, err }
	redisCfg, err := loadRedisConfig(redisDB, ttl)
	if err != nil { return nil, err }
	return &Config{
		BaseURL:        getenv("BASE_URL", "http://localhost:8080"),
		StaticDir:      getenv("STATIC_DIR", "./static"),
		MigrationsPath: getenv("MIGRATIONS_PATH", "./internal/db/migrations"),
		Server: ServerConfig{Port: getenv("PORT", "8080"), ReadTimeout: readTimeout, WriteTimeout: writeTimeout, IdleTimeout: idleTimeout},
		Database: DatabaseConfig{URL: databaseURL},
		Redis: redisCfg,
		GitHub: GitHubConfig{Token: os.Getenv("GITHUB_TOKEN"), Base: getenv("GITHUB_BASE_URL", "https://api.github.com")},
		SMTP: smtpCfg,
		Scanner: ScannerConfig{Interval: interval, Workers: workers},
		Auth:    AuthConfig{APIKey: apiKey},
	}, nil
}

func loadSMTPConfig() (SMTPConfig, error) {
	smtpFrom := os.Getenv("SMTP_FROM")
	if smtpFrom == "" {
		return SMTPConfig{}, fmt.Errorf("SMTP_FROM is required")
	}

	if cloudMailinURL := os.Getenv("CLOUDMAILIN_SMTP_URL"); cloudMailinURL != "" {
		cfg, err := parseSMTPURL("CLOUDMAILIN_SMTP_URL", cloudMailinURL)
		if err != nil {
			return SMTPConfig{}, err
		}
		cfg.From = smtpFrom
		return cfg, nil
	}

	smtpHost := os.Getenv("SMTP_HOST")
	if smtpHost == "" {
		return SMTPConfig{}, fmt.Errorf("SMTP_HOST is required")
	}

	return SMTPConfig{
		Host:     smtpHost,
		Port:     getenv("SMTP_PORT", "1025"),
		Username: os.Getenv("SMTP_USERNAME"),
		Password: os.Getenv("SMTP_PASSWORD"),
		From:     smtpFrom,
		UseTLS:   getenv("SMTP_TLS", "false") == "true",
	}, nil
}

func parseSMTPURL(varName, rawURL string) (SMTPConfig, error) {
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return SMTPConfig{}, fmt.Errorf("parsing %s: %w", varName, err)
	}

	if parsed.Hostname() == "" {
		return SMTPConfig{}, fmt.Errorf("%s missing host", varName)
	}

	port := parsed.Port()
	if port == "" {
		port = "587"
	}

	password, _ := parsed.User.Password()
	startTLS := parsed.Query().Get("starttls")
	useTLS := startTLS == "" || strings.EqualFold(startTLS, "true")

	return SMTPConfig{
		Host:     parsed.Hostname(),
		Port:     port,
		Username: parsed.User.Username(),
		Password: password,
		UseTLS:   useTLS,
	}, nil
}

func loadRedisConfig(defaultDB int, ttl time.Duration) (RedisConfig, error) {
	if rawTLSURL := os.Getenv("REDIS_TLS_URL"); rawTLSURL != "" {
		return parseRedisURL("REDIS_TLS_URL", rawTLSURL, defaultDB, ttl)
	}
	if rawURL := os.Getenv("REDIS_URL"); rawURL != "" {
		return parseRedisURL("REDIS_URL", rawURL, defaultDB, ttl)
	}
	return RedisConfig{Addr: getenv("REDIS_ADDR", "localhost:6379"), Password: os.Getenv("REDIS_PASSWORD"), DB: defaultDB, TTL: ttl}, nil
}

func parseRedisURL(varName, rawURL string, defaultDB int, ttl time.Duration) (RedisConfig, error) {
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return RedisConfig{}, fmt.Errorf("parsing %s: %w", varName, err)
	}
	password, _ := parsed.User.Password()
	db := defaultDB
	if parsed.Path != "" && parsed.Path != "/" {
		parsedDB, err := strconv.Atoi(strings.TrimPrefix(parsed.Path, "/"))
		if err != nil {
			return RedisConfig{}, fmt.Errorf("parsing %s database: %w", varName, err)
		}
		db = parsedDB
	}
	return RedisConfig{
		Addr:          parsed.Host,
		Password:      password,
		DB:            db,
		UseTLS:        parsed.Scheme == "rediss",
		TLSServerName: os.Getenv("REDIS_TLS_SERVER_NAME"),
		TLSInsecureSkipVerify: getenv("REDIS_TLS_INSECURE_SKIP_VERIFY", "false") == "true",
		TTL:           ttl,
	}, nil
}

func getenv(key, fallback string) string {
	if value := os.Getenv(key); value != "" { return value }
	return fallback
}

func durationEnv(key string, fallback time.Duration) (time.Duration, error) {
	value := os.Getenv(key)
	if value == "" { return fallback, nil }
	return time.ParseDuration(value)
}

func intEnv(key string, fallback int) (int, error) {
	value := os.Getenv(key)
	if value == "" { return fallback, nil }
	parsed, err := strconv.Atoi(value)
	if err != nil { return 0, fmt.Errorf("parsing %s: %w", key, err) }
	return parsed, nil
}
