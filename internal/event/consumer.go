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
)

const (
	exchangeName = "sentiric_events"
	queueName    = "sentiric.workflow_service.events"
)

type Consumer struct {
	processor *engine.Processor
	log       zerolog.Logger
}

func NewConsumer(processor *engine.Processor, log zerolog.Logger) *Consumer {
	return &Consumer{
		processor: processor,
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

	// call.started dinliyoruz
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
	//[KRİTİK DÜZELTME]: Gelen veriyi JSON değil, Protobuf olarak parse ediyoruz.
	var callStarted eventv1.CallStartedEvent
	if err := proto.Unmarshal(d.Body, &callStarted); err != nil {
		// Başka formatta bir event gelirse gürültü yapmamak için debug basıp geçiyoruz.
		c.log.Debug().Err(err).Msg("Mesaj CallStartedEvent formatında değil, atlanıyor.")
		return
	}

	if callStarted.EventType == "call.started" {
		callID := callStarted.CallId
		c.log.Info().Str("call_id", callID).Msg("📞 Yeni çağrı yakalandı. Workflow başlatılıyor...")

		// 1. Dialplan Kararını Al
		if callStarted.DialplanResolution == nil || callStarted.DialplanResolution.Action == nil {
			c.log.Warn().Str("call_id", callID).Msg("Dialplan Action eksik! Akış durduruldu.")
			return
		}

		actionType := callStarted.DialplanResolution.Action.Action

		// 2. Medya Bilgilerini (RTP Port vb.) Al
		var rtpPort uint32
		var rtpTarget string
		if callStarted.MediaInfo != nil {
			rtpPort = callStarted.MediaInfo.ServerRtpPort
			rtpTarget = callStarted.MediaInfo.CallerRtpAddr
		}

		// 3. Workflow JSON'unu Üret ve Motora Ver
		workflowDef := c.generateMockWorkflow(actionType)
		c.processor.StartWorkflow(ctx, callID, rtpPort, rtpTarget, workflowDef)
	}
}

func (c *Consumer) generateMockWorkflow(actionType string) string {
	// Gelen enum formatlarını da, string formatlarını da kapsayacak şekilde kontrol:
	if actionType == "ECHO_TEST" || actionType == "ACTION_TYPE_ECHO_TEST" {
		return `{
			"id": "wf_echo_mock",
			"start_node": "step_echo",
			"steps": {
				"step_echo": { "type": "execute_command", "params": { "command": "media.enable_echo" }, "next": "step_wait" },
				"step_wait": { "type": "wait", "params": { "duration_seconds": "60" } }
			}
		}`
	}

	if actionType == "START_AI_CONVERSATION" || actionType == "ACTION_TYPE_START_AI_CONVERSATION" {
		return `{
			"id": "wf_ai_mock",
			"start_node": "step_handoff",
			"steps": {
				"step_handoff": { "type": "handover_to_agent", "params": { "mode": "duplex" } }
			}
		}`
	}

	return `{"id": "wf_empty", "start_node": "end", "steps": { "end": { "type": "hangup" } }}`
}
