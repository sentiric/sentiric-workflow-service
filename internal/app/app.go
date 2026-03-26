// sentiric-workflow-service/internal/app/app.go
package app

import (
	"context"
	"os"
	"os/signal"
	"sync"
	"syscall"

	"github.com/rabbitmq/amqp091-go"
	"github.com/rs/zerolog"
	"github.com/sentiric/sentiric-workflow-service/internal/client"
	"github.com/sentiric/sentiric-workflow-service/internal/config"
	"github.com/sentiric/sentiric-workflow-service/internal/database"
	"github.com/sentiric/sentiric-workflow-service/internal/engine"
	"github.com/sentiric/sentiric-workflow-service/internal/event"
	"github.com/sentiric/sentiric-workflow-service/internal/repository"
)

func Run(cfg *config.Config, log zerolog.Logger) {
	pgPool, err := database.NewPostgresConnection(cfg.PostgresURL, log)
	if err != nil {
		log.Fatal().Str("event", "DB_CONN_FAIL").Err(err).Msg("Postgres connection failed")
	}
	defer pgPool.Close()

	redisClient, err := database.NewRedisClient(cfg.RedisURL, log)
	if err != nil {
		log.Fatal().Str("event", "REDIS_CONN_FAIL").Err(err).Msg("Redis connection failed")
	}

	clients, err := client.NewClients(cfg, log)
	if err != nil {
		log.Fatal().Str("event", "GRPC_CLIENT_FAIL").Err(err).Msg("gRPC Clients init failed")
	}
	defer clients.Close()

	// [ARCH-COMPLIANCE] Anti-Pattern Fix: RabbitMQ bağlantısı sadece 1 kez kurulur.
	amqpConn, err := amqp091.Dial(cfg.RabbitMQURL)
	if err != nil {
		log.Fatal().Str("event", "AMQP_DIAL_FAIL").Err(err).Msg("RabbitMQ initial connection failed")
	}
	defer amqpConn.Close()

	repo := repository.NewWorkflowRepository(pgPool, redisClient.Client, log)

	// Bağlantı nesnesi Processor'a geçiriliyor
	processor := engine.NewProcessor(redisClient.Client, repo, clients, amqpConn, log)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var wg sync.WaitGroup
	consumer := event.NewConsumer(processor, repo, log)

	if err := consumer.Start(ctx, amqpConn, &wg); err != nil {
		log.Fatal().Str("event", "AMQP_CONSUMER_FAIL").Err(err).Msg("RabbitMQ Consumer başlatılamadı")
	}

	log.Info().Str("event", "SERVICE_READY").Msg("✅ Workflow Service Çalışıyor (DB Integrated). Olay bekleniyor...")

	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)
	<-sigChan

	log.Warn().Str("event", "SHUTDOWN_SIGNAL_RECEIVED").Msg("Kapatma sinyali alındı. Servis durduruluyor...")
	cancel()
	wg.Wait()
	log.Info().Str("event", "SERVICE_STOPPED").Msg("Servis güvenle kapatıldı.")
}
