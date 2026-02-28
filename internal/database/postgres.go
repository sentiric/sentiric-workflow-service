package database

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/rs/zerolog"
)

func NewPostgresConnection(url string, log zerolog.Logger) (*pgxpool.Pool, error) {
	log.Info().Msg("🐘 PostgreSQL bağlantısı başlatılıyor...")

	config, err := pgxpool.ParseConfig(url)
	if err != nil {
		return nil, fmt.Errorf("postgres config parse error: %w", err)
	}

	// Bağlantı havuzu ayarları
	config.MaxConns = 10
	config.MinConns = 2
	config.MaxConnLifetime = time.Hour
	config.MaxConnIdleTime = 30 * time.Minute

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	pool, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		return nil, fmt.Errorf("postgres connection error: %w", err)
	}

	if err := pool.Ping(ctx); err != nil {
		return nil, fmt.Errorf("postgres ping failed: %w", err)
	}

	log.Info().Msg("✅ PostgreSQL bağlantısı sağlandı.")
	return pool, nil
}
