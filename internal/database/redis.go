package database

import (
	"context"
	"time"

	"github.com/redis/go-redis/v9"
	"github.com/rs/zerolog"
)

type RedisClient struct {
	Client *redis.Client
}

func NewRedisClient(url string, log zerolog.Logger) *RedisClient {
	log.Info().Str("event", "REDIS_CONNECTING").Msg("🔴 Redis bağlantısı başlatılıyor...")
	opts, _ := redis.ParseURL(url)
	client := redis.NewClient(opts)

	go func() {
		for {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			err := client.Ping(ctx).Err()
			cancel()
			if err == nil {
				log.Info().Str("event", "REDIS_CONNECTED").Msg("✅ Redis bağlantısı sağlandı.")
				return
			}
			log.Warn().Str("event", "REDIS_RETRY").Err(err).Msg("Redis'e bağlanılamadı, yeniden denenecek...")
			time.Sleep(5 * time.Second)
		}
	}()

	return &RedisClient{Client: client}
}
