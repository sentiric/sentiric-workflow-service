// Dosya: sentiric-workflow-service/internal/event/consumer.go
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
	dlxName      = "sentiric_events.failed"
	dlqName      = "sentiric.workflow_service.failed"
)

type Consumer struct {
	processor *engine.Processor
	repo      *repository.WorkflowRepository
	log       zerolog.Logger
}

func NewConsumer(processor *engine.Processor, repo *repository.WorkflowRepository, log zerolog.Logger) *Consumer {
	return &Consumer{processor: processor, repo: repo, log: log}
}

func (c *Consumer) Start(ctx context.Context, conn *amqp091.Connection, wg *sync.WaitGroup) error {
	ch, err := conn.Channel()
	if err != nil {
		return err
	}

	//[ARCH-COMPLIANCE] constraints.yaml: dead_letter_queue kuralı gereği DLX ve DLQ tanımlanır.
	_ = ch.ExchangeDeclare(dlxName, "topic", true, false, false, false, nil)
	_, _ = ch.QueueDeclare(dlqName, true, false, false, false, nil)
	_ = ch.QueueBind(dlqName, "#", dlxName, false, nil)

	_ = ch.ExchangeDeclare(exchangeName, "topic", true, false, false, false, nil)

	args := amqp091.Table{
		"x-dead-letter-exchange": dlxName,
	}
	q, _ := ch.QueueDeclare(queueName, true, false, false, false, args)

	_ = ch.QueueBind(q.Name, "call.started", exchangeName, false, nil)
	_ = ch.QueueBind(q.Name, "call.media.playback.finished", exchangeName, false, nil)
	_ = ch.QueueBind(q.Name, "call.ended", exchangeName, false, nil)

	msgs, _ := ch.Consume(q.Name, "", false, false, false, false, nil)

	c.log.Info().Str("event", "AMQP_CONSUMER_STARTED").Msg("🐰 Workflow Consumer: Olaylar Dinleniyor (SRE DLX Aktif)...")

	wg.Add(1)
	go func() {
		defer wg.Done()
		defer ch.Close()

		for {
			select {
			case <-ctx.Done():
				return
			case d, ok := <-msgs:
				if !ok {
					return
				}

				func() {
					defer func() {
						if r := recover(); r != nil {
							c.log.Error().Str("event", "AMQP_MESSAGE_PANIC").Interface("panic", r).Msg("CRITICAL: Workflow mesajı işlerken panikledi. DLX'e atılıyor.")
							_ = d.Nack(false, false)
						}
					}()

					c.routeMessage(ctx, d)
					_ = d.Ack(false)
				}()
			}
		}
	}()
	return nil
}

func (c *Consumer) routeMessage(ctx context.Context, d amqp091.Delivery) {
	switch d.RoutingKey {
	case "call.started":
		c.handleCallStarted(ctx, d.Body)
	case "call.media.playback.finished":
		c.handlePlaybackFinished(ctx, d.Body)
	case "call.ended":
		c.handleCallEnded(ctx, d.Body)
	default:
		// [ARCH-COMPLIANCE] ARCH-007
		c.log.Debug().Str("event", "AMQP_IGNORED_EVENT").Str("routing_key", d.RoutingKey).Msg("İlgilenilmeyen event geldi, geçiliyor.")
	}
}

func (c *Consumer) handlePlaybackFinished(ctx context.Context, body []byte) {
	var event eventv1.GenericEvent
	if err := proto.Unmarshal(body, &event); err != nil {
		return
	}
	c.log.Info().Str("event", "PLAYBACK_FINISHED_RESUME").Str("call_id", event.TraceId).Msg("▶️ Playback bitti. Asenkron akış devam ettiriliyor...")
	c.processor.ResumeWorkflow(ctx, event.TraceId, "playback_finished")
}

func (c *Consumer) handleCallEnded(ctx context.Context, body []byte) {
	var event eventv1.CallEndedEvent
	if err := proto.Unmarshal(body, &event); err != nil {
		return
	}
	c.log.Info().Str("event", "CALL_ENDED_SESSION_CLOSE").Str("call_id", event.CallId).Msg("📞 Çağrı sonlandı. Oturum kapatılıyor.")
	c.repo.UpdateSessionStatus(ctx, event.CallId, "COMPLETED")
}

func (c *Consumer) handleCallStarted(ctx context.Context, body []byte) {
	var callStarted eventv1.CallStartedEvent
	if err := proto.Unmarshal(body, &callStarted); err != nil {
		return
	}

	l := c.log.With().Str("trace_id", callStarted.TraceId).Str("call_id", callStarted.CallId).Logger()

	res := callStarted.GetDialplanResolution()
	if res == nil || res.Action == nil {
		return
	}

	dialplanDef, ok := res.Action.ActionData["definition"]
	if !ok || dialplanDef == "" {
		targetWfID := ""
		if id, exists := res.Action.ActionData["workflow_id"]; exists && id != "" {
			targetWfID = id
		} else {
			targetWfID = repository.MapActionToWorkflowID(res.Action.Action)
		}

		if targetWfID != "" {
			// [ARCH-COMPLIANCE] ARCH-007
			l.Info().Str("event", "WF_DEFINITION_LOADING").Str("wf_id", targetWfID).Msg("📂 Veritabanından Workflow tanımı yükleniyor...")
			if def, err := c.repo.GetWorkflowDefinition(ctx, targetWfID); err == nil {
				dialplanDef = def
			} else {
				l.Error().Str("event", "WF_DEFINITION_NOT_FOUND").Err(err).Str("wf_id", targetWfID).Msg("❌ Workflow DB'de bulunamadı!")
				dialplanDef = `{"id": "wf_fallback", "start_node": "end", "steps": {"end": {"type": "hangup"}}}`
			}
		} else {
			return
		}
	}

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
