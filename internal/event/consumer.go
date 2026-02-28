// sentiric-workflow-service/internal/event/consumer.go
package event

import (
	"context"
	"encoding/json"
	"sync"

	"github.com/rabbitmq/amqp091-go"
	"github.com/rs/zerolog"
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

	// Sadece call.started ve dtmf (ileride) dinliyoruz
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
	// Gelen veriyi (JSON) map olarak parse et (Tip bağımsız)
	var payload map[string]interface{}
	if err := json.Unmarshal(d.Body, &payload); err != nil {
		c.log.Error().Err(err).Msg("Geçersiz RabbitMQ payload")
		return
	}

	eventType, _ := payload["eventType"].(string)
	callID, _ := payload["callId"].(string)

	if eventType == "call.started" {
		c.log.Info().Str("call_id", callID).Msg("📞 Yeni çağrı yakalandı. Workflow başlatılıyor...")

		// 1. Dialplan Resolution verisini al
		resolution, ok := payload["dialplanResolution"].(map[string]interface{})
		if !ok {
			c.log.Warn().Str("call_id", callID).Msg("Dialplan çözünürlüğü yok. Varsayılan akışa geçiliyor.")
			return
		}

		// 2. Aksiyonu Bul
		action, ok := resolution["action"].(map[string]interface{})
		if !ok {
			return
		}

		actionType, _ := action["action"].(string)

		//[GEÇİCİ KÖPRÜ (BRIDGE) MANTIĞI]:
		// Tam JSON veritabanına geçene kadar, gelen Action string'ini Workflow motoruna paslıyoruz.
		// İleride bu actionType, doğrudan veritabanındaki "Workflow ID" olacak.

		workflowDef := c.generateMockWorkflow(actionType)
		c.processor.StartWorkflow(ctx, callID, workflowDef)
	}
}

// generateMockWorkflow: Veritabanı bağlantısı tam kurulana kadar (MVP için) JSON akışları üretir.
func (c *Consumer) generateMockWorkflow(actionType string) string {
	if actionType == "ECHO_TEST" {
		return `{
			"id": "wf_echo_mock",
			"start_node": "step_echo",
			"steps": {
				"step_echo": { "type": "execute_command", "params": { "command": "media.enable_echo" }, "next": "step_wait" },
				"step_wait": { "type": "wait", "params": { "duration_seconds": "60" } }
			}
		}`
	}

	if actionType == "START_AI_CONVERSATION" {
		return `{
			"id": "wf_ai_mock",
			"start_node": "step_handoff",
			"steps": {
				"step_handoff": { "type": "handover_to_agent", "params": { "mode": "duplex" } }
			}
		}`
	}

	// Fallback
	return `{"id": "wf_empty", "start_node": "end", "steps": { "end": { "type": "hangup" } }}`
}
