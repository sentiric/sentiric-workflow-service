package database

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/rs/zerolog"
)

func NewPostgresConnection(url string, log zerolog.Logger) *pgxpool.Pool {
	log.Info().Str("event", "POSTGRES_CONNECTING").Msg("🐘 PostgreSQL bağlantısı başlatılıyor...")

	config, _ := pgxpool.ParseConfig(url)
	config.MaxConns = 10
	config.MinConns = 2
	config.MaxConnLifetime = time.Hour
	config.MaxConnIdleTime = 30 * time.Minute

	pool, _ := pgxpool.NewWithConfig(context.Background(), config)

	go func() {
		for {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			err := pool.Ping(ctx)
			cancel()
			if err == nil {
				log.Info().Str("event", "POSTGRES_CONNECTED").Msg("✅ PostgreSQL bağlantısı sağlandı.")
				return
			}
			log.Warn().Str("event", "POSTGRES_RETRY").Err(err).Msg("PostgreSQL bağlantısı yok, yeniden denenecek...")
			time.Sleep(5 * time.Second)
		}
	}()

	return pool
}
