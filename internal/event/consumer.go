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
	"github.com/sentiric/sentiric-workflow-service/internal/repository" // YENİ
)

const (
	exchangeName = "sentiric_events"
	queueName    = "sentiric.workflow_service.events"
)

type Consumer struct {
	processor *engine.Processor
	repo      *repository.WorkflowRepository // YENİ: Repo erişimi
	log       zerolog.Logger
}

// Constructor güncellendi
func NewConsumer(processor *engine.Processor, repo *repository.WorkflowRepository, log zerolog.Logger) *Consumer {
	return &Consumer{
		processor: processor,
		repo:      repo,
		log:       log,
	}
}

func (c *Consumer) Start(ctx context.Context, url string, wg *sync.WaitGroup) error {
	conn, err := amqp091.Dial(url)
	if err != nil {
		return err
	}

	ch, err := conn.Channel()
	if err != nil {
		return err
	}

	err = ch.ExchangeDeclare(exchangeName, "topic", true, false, false, false, nil)
	if err != nil {
		return err
	}

	q, err := ch.QueueDeclare(queueName, true, false, false, false, nil)
	if err != nil {
		return err
	}

	err = ch.QueueBind(q.Name, "call.started", exchangeName, false, nil)
	if err != nil {
		return err
	}

	msgs, err := ch.Consume(q.Name, "", false, false, false, false, nil)
	if err != nil {
		return err
	}

	c.log.Info().Msg("🐰 Workflow Consumer: RabbitMQ dinleniyor...")

	wg.Add(1)
	go func() {
		defer wg.Done()
		defer ch.Close()
		defer conn.Close()

		for {
			select {
			case <-ctx.Done():
				c.log.Info().Msg("Workflow Consumer durduruluyor.")
				return
			case d, ok := <-msgs:
				if !ok {
					c.log.Error().Msg("RabbitMQ kanalı kapandı.")
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
		c.log.Debug().Err(err).Msg("Mesaj CallStartedEvent formatında değil, atlanıyor.")
		return
	}

	l := c.log.With().
		Str("trace_id", callStarted.TraceId).
		Str("call_id", callStarted.CallId).
		Logger()

	if callStarted.EventType != "call.started" {
		return
	}

	res := callStarted.GetDialplanResolution()
	if res == nil || res.Action == nil {
		l.Error().Msg("❌ CRITICAL: Dialplan Resolution bilgisi olmadan 'call.started' olayı alındı!")
		return
	}

	// [AKILLI WORKFLOW YÖNETİMİ]
	dialplanDef, ok := res.Action.ActionData["definition"]

	if !ok || dialplanDef == "" {
		// Payload içinde JSON yoksa, DB'den bulmaya çalış
		actionType := res.Action.Action // Örn: ECHO_TEST
		targetWfID := repository.MapActionToWorkflowID(actionType)

		if targetWfID != "" {
			l.Info().Str("wf_id", targetWfID).Msg("📂 Veritabanından Workflow tanımı yükleniyor...")
			if def, err := c.repo.GetWorkflowDefinition(ctx, targetWfID); err == nil {
				dialplanDef = def
			} else {
				l.Error().Err(err).Str("wf_id", targetWfID).Msg("❌ Workflow DB'de bulunamadı!")
				// Fallback: Acil durum mock'u (Servisin çökmemesi için)
				dialplanDef = `{"id": "wf_fallback", "start_node": "end", "steps": {"end": {"type": "hangup"}}}`
			}
		} else {
			// Eğer Mapping yoksa (Örn: BRIDGE_CALL), bu bir "Aksiyon"dur, Workflow değildir.
			// Workflow servisi burada devreden çıkmalıdır.
			l.Info().Str("action", actionType).Msg("ℹ️ Bu aksiyon tipi için Workflow tanımı gerekmiyor.")
			return
		}
	}

	l.Info().
		Str("tenant_id", res.TenantId).
		Str("dialplan_id", res.DialplanId).
		Msg("📞 Workflow Motoru tetikleniyor...")

	c.processor.StartWorkflow(
		ctx,
		callStarted.CallId,
		callStarted.TraceId,
		callStarted.GetMediaInfo().GetServerRtpPort(),
		callStarted.GetMediaInfo().GetCallerRtpAddr(),
		dialplanDef,
		res.Action.ActionData,
	)
}
