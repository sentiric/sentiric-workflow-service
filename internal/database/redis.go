package database

import (
	"context"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
	"github.com/rs/zerolog"
)

type RedisClient struct {
	Client *redis.Client
}

func NewRedisClient(url string, log zerolog.Logger) (*RedisClient, error) {
	log.Info().Msg("🔴 Redis bağlantısı başlatılıyor...")

	opts, err := redis.ParseURL(url)
	if err != nil {
		return nil, fmt.Errorf("redis url parse error: %w", err)
	}

	client := redis.NewClient(opts)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := client.Ping(ctx).Err(); err != nil {
		return nil, fmt.Errorf("redis ping failed: %w", err)
	}

	log.Info().Msg("✅ Redis bağlantısı sağlandı.")
	return &RedisClient{Client: client}, nil
}
