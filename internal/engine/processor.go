// Dosya: sentiric-workflow-service/internal/engine/processor.go
package engine

// [ARCH-COMPLIANCE] RabbitMQ Payload'ları GenericEvent (Protobuf) formatında gönderilmeli.
import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/rabbitmq/amqp091-go"
	"github.com/redis/go-redis/v9"
	"github.com/rs/zerolog"

	agentv1 "github.com/sentiric/sentiric-contracts/gen/go/sentiric/agent/v1"
	eventv1 "github.com/sentiric/sentiric-contracts/gen/go/sentiric/event/v1"
	mediav1 "github.com/sentiric/sentiric-contracts/gen/go/sentiric/media/v1"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/sentiric/sentiric-workflow-service/internal/client"
	"github.com/sentiric/sentiric-workflow-service/internal/repository"
	"go.opentelemetry.io/otel/trace"
	"google.golang.org/grpc/metadata"
)

type Processor struct {
	redis    *redis.Client
	repo     *repository.WorkflowRepository
	clients  *client.GrpcClients
	log      zerolog.Logger
	amqpConn *amqp091.Connection // [ARCH-COMPLIANCE] Shared AMQP Connection
}

func NewProcessor(r *redis.Client, repo *repository.WorkflowRepository, c *client.GrpcClients, amqpConn *amqp091.Connection, l zerolog.Logger) *Processor {
	return &Processor{redis: r, repo: repo, clients: c, amqpConn: amqpConn, log: l}
}

// StartWorkflow içindeki Logger oluşturma bloğu güncellendi:
func (p *Processor) StartWorkflow(ctx context.Context, callID, traceID string, rtpPort uint32, rtpTarget string, workflowDefJSON string, actionData map[string]string) {
	var wf Workflow
	if err := json.Unmarshal([]byte(workflowDefJSON), &wf); err != nil {
		p.log.Error().Str("event", "WF_JSON_PARSE_ERROR").Err(err).Msg("❌ Workflow JSON parse hatası")
		return
	}

	if wf.ID == "" {
		if id, ok := actionData["workflow_id"]; ok {
			wf.ID = id
		} else {
			wf.ID = "wf_unknown"
		}
	}

	// [ARCH-COMPLIANCE] SUTS v4.0: span_id kuralı uygulandı.
	spanID := trace.SpanFromContext(ctx).SpanContext().SpanID().String()
	if spanID == "0000000000000000" {
		spanID = "" // Span yoksa boş bırak (SUTS şeması null veya string kabul eder)
	}

	l := p.log.With().
		Str("trace_id", traceID).
		Str("span_id", spanID).
		Str("call_id", callID).
		Logger()

	l.Info().Str("event", "WF_STARTING").Str("wf_id", wf.ID).Msg("🚀 Workflow Başlatılıyor")

	if err := p.repo.CreateSession(ctx, callID, wf.ID, wf.StartNode, traceID, rtpPort, rtpTarget); err != nil {
		l.Warn().Str("event", "WF_SESSION_CREATE_FAIL").Err(err).Msg("⚠️ Session DB'ye kaydedilemedi.")
		return
	}

	if rec, ok := actionData["record"]; ok && rec == "true" {
		l.Info().Str("event", "WF_RECORD_INIT").Msg("🎙️ Çağrı kayıt izni tespit edildi. Media Service üzerinde kayıt başlatılıyor...")

		outCtx := metadata.AppendToOutgoingContext(context.Background(), "x-trace-id", traceID)

		//[ARCH-COMPLIANCE] timeouts kuralı
		timeoutCtx, cancel := context.WithTimeout(outCtx, 5*time.Second)
		defer cancel()

		_, err := p.clients.Media.StartRecording(timeoutCtx, &mediav1.StartRecordingRequest{
			ServerRtpPort: rtpPort,
			OutputUri:     "s3://sentiric/recordings",
			CallId:        callID,
			TraceId:       traceID,
		})

		if err != nil {
			l.Error().Str("event", "WF_RECORD_START_FAIL").Err(err).Msg("❌ Media Service üzerinde kayıt başlatılamadı.")
		} else {
			l.Info().Str("event", "WF_RECORD_STARTED").Msg("✅ Ses kaydı (RTP) başarıyla başlatıldı.")
		}
	}

	p.executeStep(ctx, l, callID, traceID, rtpPort, rtpTarget, wf.StartNode, &wf, actionData)
}

func (p *Processor) ResumeWorkflow(ctx context.Context, callID, trigger string) {
	session, err := p.repo.GetSession(ctx, callID)
	if err != nil {
		p.log.Warn().Str("event", "WF_SESSION_NOT_FOUND").Str("call_id", callID).Msg("Session bulunamadığı için akış devam ettirilemedi.")
		return
	}

	if session["status"] != "RUNNING" {
		p.log.Debug().Str("event", "WF_SESSION_NOT_RUNNING").Str("call_id", callID).Msg("Session RUNNING değil, Resume iptal edildi.")
		return
	}

	wfJSON, err := p.repo.GetWorkflowDefinition(ctx, session["workflow_id"])
	if err != nil {
		return
	}

	var wf Workflow
	if err := json.Unmarshal([]byte(wfJSON), &wf); err != nil {
		return
	}

	currentStep, exists := wf.Steps[session["current_step"]]
	if !exists || currentStep.Next == "" {
		p.repo.UpdateSessionStatus(ctx, callID, "COMPLETED")
		return
	}

	traceID := session["trace_id"]
	rtpPort, _ := strconv.ParseUint(session["rtp_port"], 10, 32)
	rtpTarget := session["rtp_target"]

	l := p.log.With().Str("trace_id", traceID).Str("call_id", callID).Logger()
	l.Info().Str("event", "WF_RESUMED").Str("trigger", trigger).Str("next_step", currentStep.Next).Msg("♻️ Akış Resume edildi.")

	p.executeStep(ctx, l, callID, traceID, uint32(rtpPort), rtpTarget, currentStep.Next, &wf, nil)
}

func (p *Processor) executeStep(ctx context.Context, l zerolog.Logger, callID, traceID string, rtpPort uint32, rtpTarget string, stepID string, wf *Workflow, actionData map[string]string) {
	step, exists := wf.Steps[stepID]
	if !exists {
		l.Warn().Str("event", "WF_STEP_NOT_FOUND").Str("step", stepID).Msg("Step bulunamadı, akış durdu.")
		p.repo.UpdateSessionStatus(ctx, callID, "ERROR")
		return
	}

	p.repo.UpdateSessionStep(ctx, callID, stepID)
	outCtx := metadata.AppendToOutgoingContext(context.Background(), "x-trace-id", traceID)
	l.Debug().Str("event", "WF_STEP_PROCESSING").Str("step", stepID).Str("type", step.Type).Msg("Adım işleniyor...")

	isAsync := false

	switch step.Type {

	case "play_audio":
		isAsync = true
		lang := "tr"
		if actionData != nil {
			if l, ok := actionData["language"]; ok && l != "" {
				lang = l
			}
		}

		var audioFile string
		if annID, ok := step.Params["announcement_id"]; ok {
			tenantID := "system"
			if t, ok := actionData["tenant_id"]; ok {
				tenantID = t
			}
			audioFile, _ = p.repo.GetAnnouncementPath(ctx, annID, tenantID, lang)
		} else if file, ok := step.Params["file"]; ok {
			audioFile = file
		}

		if audioFile != "" {
			l.Info().Str("event", "WF_PLAY_AUDIO_SENT").Str("file", audioFile).Msg("🔊 PlayAudio gönderiliyor...")

			timeoutCtx, cancel := context.WithTimeout(outCtx, 5*time.Second)
			defer cancel()

			_, err := p.clients.Media.PlayAudio(timeoutCtx, &mediav1.PlayAudioRequest{
				AudioUri:      fmt.Sprintf("file://%s", audioFile),
				ServerRtpPort: rtpPort,
				RtpTargetAddr: rtpTarget,
			})
			if err != nil {
				l.Warn().Str("event", "WF_PLAY_AUDIO_FAIL").Err(err).Msg("Media.PlayAudio başarısız.")
				isAsync = false
			}
		} else {
			l.Warn().Str("event", "WF_AUDIO_FILE_MISSING").Msg("⚠️ Çalınacak ses dosyası bulunamadı, adım atlanıyor.")
			isAsync = false
		}

	case "execute_command":
		if cmd, ok := step.Params["command"]; ok && cmd == "media.enable_echo" {
			l.Info().Str("event", "WF_ECHO_TEST_SENT").Msg("🔊 Workflow: Echo Test komutu gönderiliyor...")

			timeoutCtx, cancel := context.WithTimeout(outCtx, 5*time.Second)
			defer cancel()

			_, err := p.clients.Media.PlayAudio(timeoutCtx, &mediav1.PlayAudioRequest{
				AudioUri:      "control://enable_echo",
				ServerRtpPort: rtpPort,
				RtpTargetAddr: rtpTarget,
			})
			if err != nil {
				l.Error().Str("event", "WF_ECHO_TEST_FAIL").Err(err).Msg("Media.PlayAudio (Echo) başarısız oldu.")
			}
		}

	case "handover_to_agent":
		l.Info().Str("event", "WF_HANDOVER_INIT").Msg("🤖 Çağrı Agent Service'e devrediliyor (Handover)...")
		dialplanID := "DP_DEMO_AI_ASSISTANT"
		if actionData != nil {
			if dpID, ok := actionData["dialplan_id"]; ok && dpID != "" {
				dialplanID = dpID
			}
		}

		var err error
		for attempt := 1; attempt <= 5; attempt++ {
			timeoutCtx, cancel := context.WithTimeout(outCtx, 5*time.Second)
			_, err = p.clients.Agent.ProcessCallStart(timeoutCtx, &agentv1.ProcessCallStartRequest{
				CallId:     callID,
				DialplanId: dialplanID,
			})
			cancel()

			if err == nil {
				break
			}
			if strings.Contains(err.Error(), "NotFound") || strings.Contains(err.Error(), "not found") {
				time.Sleep(time.Millisecond * 150)
				continue
			}
			break
		}

		if err != nil {
			p.repo.UpdateSessionStatus(ctx, callID, "ERROR")

			// [ARCH-COMPLIANCE] Connection leak engellendi, Payload Protobuf'a çevrildi
			go func(conn *amqp091.Connection, cid string, reasonStr string) {
				if conn == nil || conn.IsClosed() {
					return
				}
				if ch, err := conn.Channel(); err == nil {
					defer ch.Close()

					payloadJSON := fmt.Sprintf(`{"callId":"%s","reason":"%s"}`, cid, reasonStr)

					pbEvent := &eventv1.GenericEvent{
						EventType:   "call.terminate.request",
						TraceId:     cid,
						Timestamp:   timestamppb.Now(),
						TenantId:    "system",
						PayloadJson: payloadJSON,
					}

					body, _ := proto.Marshal(pbEvent)

					_ = ch.PublishWithContext(context.Background(), "sentiric_events", "call.terminate.request", false, false, amqp091.Publishing{
						ContentType: "application/protobuf",
						Body:        body,
					})
				}
			}(p.amqpConn, callID, "system_terminated") // "hangup" case'i için buraya "workflow_hangup" yazın.
			return
		}

		p.repo.UpdateSessionStatus(ctx, callID, "HANDOVER_AGENT")
		l.Info().Str("event", "WF_HANDOVER_SUCCESS").Str("call_id", callID).Msg("✅ Kontrol Agent'ta.")
		return

	case "wait":
		durationSecs := 2
		if dsStr, ok := step.Params["duration_seconds"]; ok {
			if parsed, err := strconv.Atoi(dsStr); err == nil {
				durationSecs = parsed
			}
		}
		l.Info().Str("event", "WF_WAITING").Int("seconds", durationSecs).Msg("⏳ Workflow bekletiliyor...")
		time.Sleep(time.Duration(durationSecs) * time.Second)

	case "hangup":
		p.repo.UpdateSessionStatus(ctx, callID, "COMPLETED")
		l.Info().Str("event", "WF_HANGUP_RECEIVED").Str("call_id", callID).Msg("🛑 Hangup komutu alındı.")

		// [ARCH-COMPLIANCE] Connection leak engellendi, Payload Protobuf'a çevrildi
		go func(conn *amqp091.Connection, cid string, reasonStr string) {
			if conn == nil || conn.IsClosed() {
				return
			}
			if ch, err := conn.Channel(); err == nil {
				defer ch.Close()

				payloadJSON := fmt.Sprintf(`{"callId":"%s","reason":"%s"}`, cid, reasonStr)

				pbEvent := &eventv1.GenericEvent{
					EventType:   "call.terminate.request",
					TraceId:     cid,
					Timestamp:   timestamppb.Now(),
					TenantId:    "system",
					PayloadJson: payloadJSON,
				}

				body, _ := proto.Marshal(pbEvent)

				_ = ch.PublishWithContext(context.Background(), "sentiric_events", "call.terminate.request", false, false, amqp091.Publishing{
					ContentType: "application/protobuf",
					Body:        body,
				})
			}
		}(p.amqpConn, callID, "workflow_hangup") // "hangup" case'i için buraya "workflow_hangup" yazın.
		return
	}

	if !isAsync {
		if step.Next != "" {
			p.executeStep(ctx, l, callID, traceID, rtpPort, rtpTarget, step.Next, wf, actionData)
		} else {
			p.repo.UpdateSessionStatus(ctx, callID, "COMPLETED")
			l.Info().Str("event", "WF_COMPLETED").Str("call_id", callID).Msg("✅ Workflow Tamamlandı.")
		}
	} else {
		l.Info().Str("event", "WF_ASYNC_PAUSED").Msg("⏸️ Workflow asenkron olayı (RabbitMQ) beklemek üzere RAM'den bırakıldı...")
	}
}
