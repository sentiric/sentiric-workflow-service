package app

import (
	"context"
	"os"
	"os/signal"
	"sync"
	"syscall"

	"github.com/rs/zerolog"
	"github.com/sentiric/sentiric-workflow-service/internal/client"
	"github.com/sentiric/sentiric-workflow-service/internal/config"
	"github.com/sentiric/sentiric-workflow-service/internal/database"
	"github.com/sentiric/sentiric-workflow-service/internal/engine"
	"github.com/sentiric/sentiric-workflow-service/internal/event"
	"github.com/sentiric/sentiric-workflow-service/internal/queue"
	"github.com/sentiric/sentiric-workflow-service/internal/repository"
)

func Run(cfg *config.Config, log zerolog.Logger) {
	// [ARCH-COMPLIANCE] Çökme (Crash) tamamen kaldırıldı, sonsuz döngüde async dener.
	pgPool := database.NewPostgresConnection(cfg.PostgresURL, log)
	defer pgPool.Close()

	redisClient := database.NewRedisClient(cfg.RedisURL, log)

	clients, err := client.NewClients(cfg, log)
	if err != nil {
		log.Fatal().Str("event", "GRPC_CLIENT_FAIL").Err(err).Msg("gRPC Clients init failed")
	}
	defer clients.Close()

	rmq := queue.NewRabbitMQ(cfg.RabbitMQURL, log)

	repo := repository.NewWorkflowRepository(pgPool, redisClient.Client, log)
	processor := engine.NewProcessor(redisClient.Client, repo, clients, rmq, log)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var wg sync.WaitGroup
	consumer := event.NewConsumer(processor, repo, log)

	rmq.SetTopologyAndConsumer(consumer.SetupTopology, consumer.Consume)
	go rmq.Start(ctx, &wg)

	log.Info().Str("event", "SERVICE_READY").Msg("✅ Workflow Service Çalışıyor (Mimarisi Güçlendirildi). Olay bekleniyor...")

	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)
	<-sigChan

	log.Warn().Str("event", "SHUTDOWN_SIGNAL_RECEIVED").Msg("Kapatma sinyali alındı. Servis durduruluyor...")
	cancel()
	wg.Wait()
	log.Info().Str("event", "SERVICE_STOPPED").Msg("Servis güvenle kapatıldı.")
}
