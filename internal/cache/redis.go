package cache

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"time"

	"github.com/redis/go-redis/v9"
	"github.com/isprutfromua/ga-test/internal/config"
)

type Cache interface {
	Get(ctx context.Context, key string, target any) (bool, error)
	Set(ctx context.Context, key string, value any, ttl time.Duration) error
	Delete(ctx context.Context, key string) error
}

type RedisCache struct{ client *redis.Client }

func NewRedis(cfg config.RedisConfig) (Cache, error) {
	options := &redis.Options{Addr: cfg.Addr, Password: cfg.Password, DB: cfg.DB}
	if cfg.UseTLS {
		options.TLSConfig = &tls.Config{MinVersion: tls.VersionTLS12, ServerName: cfg.TLSServerName}
	}
	client := redis.NewClient(options)
	if err := client.Ping(context.Background()).Err(); err != nil { return nil, err }
	return &RedisCache{client: client}, nil
}

func (c *RedisCache) Get(ctx context.Context, key string, target any) (bool, error) {
	value, err := c.client.Get(ctx, key).Bytes()
	if err == redis.Nil { return false, nil }
	if err != nil { return false, err }
	return true, json.Unmarshal(value, target)
}

func (c *RedisCache) Set(ctx context.Context, key string, value any, ttl time.Duration) error {
	encoded, err := json.Marshal(value)
	if err != nil { return err }
	return c.client.Set(ctx, key, encoded, ttl).Err()
}

func (c *RedisCache) Delete(ctx context.Context, key string) error { return c.client.Del(ctx, key).Err() }
