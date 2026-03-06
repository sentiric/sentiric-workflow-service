// sentiric-workflow-service/internal/event/consumer.go
package event

import (
	"context"
	"sync"

	"github.com/rabbitmq/amqp091-go"
	"github.com/rs/zerolog"
	"google.golang.org/protobuf/proto"

	eventv1 "github.com/sentiric/sentiric-contracts/gen/go/sentiric/event/v1"
	"github.com/sentiric/sentiric-workflow-service/internal/engine"
	"github.com/sentiric/sentiric-workflow-service/internal/repository"
)

const (
	exchangeName = "sentiric_events"
	queueName    = "sentiric.workflow_service.events"
)

type Consumer struct {
	processor *engine.Processor
	repo      *repository.WorkflowRepository
	log       zerolog.Logger
}

func NewConsumer(processor *engine.Processor, repo *repository.WorkflowRepository, log zerolog.Logger) *Consumer {
	return &Consumer{processor: processor, repo: repo, log: log}
}

func (c *Consumer) Start(ctx context.Context, url string, wg *sync.WaitGroup) error {
	// ... (RabbitMQ bağlantı kodları aynı kalır) ...
	conn, err := amqp091.Dial(url)
	if err != nil {
		return err
	}
	ch, err := conn.Channel()
	if err != nil {
		return err
	}
	_ = ch.ExchangeDeclare(exchangeName, "topic", true, false, false, false, nil)
	q, _ := ch.QueueDeclare(queueName, true, false, false, false, nil)
	_ = ch.QueueBind(q.Name, "call.started", exchangeName, false, nil)
	msgs, _ := ch.Consume(q.Name, "", false, false, false, false, nil)

	c.log.Info().Msg("🐰 Workflow Consumer: RabbitMQ dinleniyor...")

	wg.Add(1)
	go func() {
		defer wg.Done()
		defer ch.Close()
		defer conn.Close()

		for {
			select {
			case <-ctx.Done():
				return
			case d, ok := <-msgs:
				if !ok {
					return
				}
				c.handleMessage(ctx, d)
				d.Ack(false)
			}
		}
	}()
	return nil
}

func (c *Consumer) handleMessage(ctx context.Context, d amqp091.Delivery) {
	var callStarted eventv1.CallStartedEvent
	if err := proto.Unmarshal(d.Body, &callStarted); err != nil {
		return
	}

	l := c.log.With().Str("trace_id", callStarted.TraceId).Str("call_id", callStarted.CallId).Logger()

	res := callStarted.GetDialplanResolution()
	if res == nil || res.Action == nil {
		return
	}

	//[MİMARİ DEVRİM - TAM UI UYUMU]
	// 1. Önce Dialplan'ın doğrudan bir workflow tanımı (JSON) gönderip göndermediğine bak.
	dialplanDef, ok := res.Action.ActionData["definition"]

	if !ok || dialplanDef == "" {
		// 2. Eğer JSON tanımı yoksa, Dialplan bir "workflow_id" göndermiş mi diye bak. (UI'dan atanan özel akışlar)
		targetWfID := ""
		if id, exists := res.Action.ActionData["workflow_id"]; exists && id != "" {
			targetWfID = id
		} else {
			// 3. O da yoksa eski legacy fallback (ECHO_TEST -> wf_system_echo)
			targetWfID = repository.MapActionToWorkflowID(res.Action.Action)
		}

		if targetWfID != "" {
			l.Info().Str("wf_id", targetWfID).Msg("📂 Veritabanından Workflow tanımı yükleniyor...")
			if def, err := c.repo.GetWorkflowDefinition(ctx, targetWfID); err == nil {
				dialplanDef = def
			} else {
				l.Error().Err(err).Str("wf_id", targetWfID).Msg("❌ Workflow DB'de bulunamadı!")
				dialplanDef = `{"id": "wf_fallback", "start_node": "end", "steps": {"end": {"type": "hangup"}}}`
			}
		} else {
			// Yönlendirme (BRIDGE) gibi işler Workflow gerektirmez.
			l.Info().Str("action", res.Action.Action).Msg("ℹ️ Bu aksiyon tipi için Workflow gerekmiyor.")
			return
		}
	}

	// Workflow Motorunu Başlat
	// Res.DialplanId bilgisi ActionData'ya eklenerek Agent'a taşınması garanti altına alınır.
	actionData := res.Action.ActionData
	if actionData == nil {
		actionData = make(map[string]string)
	}
	actionData["dialplan_id"] = res.DialplanId

	c.processor.StartWorkflow(
		ctx,
		callStarted.CallId,
		callStarted.TraceId,
		callStarted.GetMediaInfo().GetServerRtpPort(),
		callStarted.GetMediaInfo().GetCallerRtpAddr(),
		dialplanDef,
		actionData,
	)
}
