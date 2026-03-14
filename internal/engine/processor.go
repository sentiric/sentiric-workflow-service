// sentiric-workflow-service/internal/engine/processor.go
package engine

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/rabbitmq/amqp091-go" // [DÜZELTME]: Import buraya taşındı
	"github.com/redis/go-redis/v9"
	"github.com/rs/zerolog"
	agentv1 "github.com/sentiric/sentiric-contracts/gen/go/sentiric/agent/v1"
	mediav1 "github.com/sentiric/sentiric-contracts/gen/go/sentiric/media/v1"
	"github.com/sentiric/sentiric-workflow-service/internal/client"
	"github.com/sentiric/sentiric-workflow-service/internal/repository"
	"google.golang.org/grpc/metadata"
)

type Processor struct {
	redis     *redis.Client
	repo      *repository.WorkflowRepository
	clients   *client.GrpcClients
	log       zerolog.Logger
	rabbitURL string
}

func NewProcessor(r *redis.Client, repo *repository.WorkflowRepository, c *client.GrpcClients, rabbitURL string, l zerolog.Logger) *Processor {
	return &Processor{redis: r, repo: repo, clients: c, rabbitURL: rabbitURL, log: l}
}

func toUint32Ptr(v uint32) *uint32 { return &v }
func toStringPtr(v string) *string { return &v }

func (p *Processor) StartWorkflow(ctx context.Context, callID, traceID string, rtpPort uint32, rtpTarget string, workflowDefJSON string, actionData map[string]string) {
	var wf Workflow
	if err := json.Unmarshal([]byte(workflowDefJSON), &wf); err != nil {
		p.log.Error().Err(err).Msg("❌ Workflow JSON parse hatası")
		return
	}

	if wf.ID == "" {
		if id, ok := actionData["workflow_id"]; ok {
			wf.ID = id
		} else {
			wf.ID = "wf_unknown"
		}
	}

	l := p.log.With().Str("trace_id", traceID).Str("call_id", callID).Logger()
	l.Info().Str("wf_id", wf.ID).Msg("🚀 Workflow Başlatılıyor")

	if err := p.repo.CreateSession(ctx, callID, wf.ID, wf.StartNode); err != nil {
		l.Warn().Err(err).Msg("⚠️ Session DB'ye kaydedilemedi ama akış devam ediyor.")
	}

	p.executeStep(ctx, l, callID, traceID, rtpPort, rtpTarget, wf.StartNode, &wf, actionData)
}

func (p *Processor) executeStep(ctx context.Context, l zerolog.Logger, callID, traceID string, rtpPort uint32, rtpTarget string, stepID string, wf *Workflow, actionData map[string]string) {
	step, exists := wf.Steps[stepID]
	if !exists {
		l.Warn().Str("step", stepID).Msg("Step bulunamadı, akış durdu.")
		p.repo.UpdateSessionStatus(ctx, callID, "ERROR")
		return
	}

	p.repo.UpdateSessionStep(ctx, callID, stepID) // Bu çağrı artık arka planda Redis'e yazar.

	outCtx := metadata.AppendToOutgoingContext(context.Background(), "x-trace-id", traceID)
	l.Debug().Str("step", stepID).Str("type", step.Type).Msg("Adım işleniyor...")

	if record, ok := actionData["record"]; ok && record == "true" {
		l.Info().Msg("🎤 Kayıt talimatı algılandı. Media Service'e StartRecording komutu gönderiliyor...")
		_, err := p.clients.Media.StartRecording(outCtx, &mediav1.StartRecordingRequest{
			CallId:        callID,
			TraceId:       traceID,
			ServerRtpPort: rtpPort,
			OutputUri:     fmt.Sprintf("s3://sentiric/recordings/%s.wav", callID),
			SampleRate:    toUint32Ptr(8000),
			Format:        toStringPtr("wav"),
		})
		if err != nil {
			l.Error().Err(err).Msg("Media.StartRecording RPC çağrısı başarısız oldu.")
		} else {
			l.Info().Msg("✅ Kayıt başarıyla başlatıldı.")
		}
		delete(actionData, "record")
	}

	switch step.Type {
	case "play_audio":
		if file, ok := step.Params["file"]; ok {
			l.Info().Str("file", file).Msg("🔊 Workflow: PlayAudio komutu gönderiliyor...")

			_, err := p.clients.Media.PlayAudio(outCtx, &mediav1.PlayAudioRequest{
				AudioUri:      fmt.Sprintf("file://%s", file),
				ServerRtpPort: rtpPort,
				RtpTargetAddr: rtpTarget,
			})

			if err != nil {
				// [YENİ]: Error yerine Warn. Çünkü kullanıcı telefonu kapatıp gitmiş olabilir.
				l.Warn().Err(err).Msg("Media.PlayAudio çağrısı başarısız oldu (Arama sonlanmış olabilir).")
			}
		}

	case "execute_command":
		if cmd, ok := step.Params["command"]; ok && cmd == "media.enable_echo" {
			l.Info().Msg("🔊 Workflow: Echo Test komutu gönderiliyor...")
			_, err := p.clients.Media.PlayAudio(outCtx, &mediav1.PlayAudioRequest{
				AudioUri:      "control://enable_echo",
				ServerRtpPort: rtpPort,
				RtpTargetAddr: rtpTarget,
			})
			if err != nil {
				l.Error().Err(err).Msg("Media.PlayAudio (Echo) RPC çağrısı başarısız oldu.")
			}
		}

	case "handover_to_agent":
		l.Info().Msg("🤖 Workflow: Çağrı Agent Service'e devrediliyor (Handover)...")
		dialplanID := "DP_DEMO_AI_ASSISTANT"
		if dpID, ok := actionData["dialplan_id"]; ok && dpID != "" {
			dialplanID = dpID
		}

		// =====================================================================
		// [CRITICAL FIX]: DISTRIBUTED RACE CONDITION GUARD
		// B2BUA'dan gelen RabbitMQ mesajının Agent Service tarafından işlenip
		// Redis'e yazılması 50-100ms sürebilir. Workflow çok hızlı olduğu için
		// gRPC isteği NotFound dönebilir. Burada akıllı bir Retry kurguluyoruz.
		// =====================================================================
		var err error
		maxRetries := 5

		for attempt := 1; attempt <= maxRetries; attempt++ {
			_, err = p.clients.Agent.ProcessCallStart(outCtx, &agentv1.ProcessCallStartRequest{
				CallId:     callID,
				DialplanId: dialplanID,
			})

			if err == nil {
				if attempt > 1 {
					l.Info().Int("attempt", attempt).Msg("✅ Agent state hazır oldu, Handover başarılı.")
				}
				break // Başarılı, döngüden çık.
			}

			// Eğer hata NotFound ise (Yani Redis State henüz oluşmadıysa) bekle ve tekrar dene
			if strings.Contains(err.Error(), "NotFound") || strings.Contains(err.Error(), "not found") {
				l.Warn().
					Int("attempt", attempt).
					Int("max_retries", maxRetries).
					Msg("⏳ Agent state henüz Redis'e yazılmamış. 150ms bekleniyor... (Race Condition Guard)")

				time.Sleep(time.Millisecond * 150)
				continue
			} else {
				// Başka bir teknik hata varsa (örn: Bağlantı koptu) boşuna bekleme
				break
			}
		}

		if err != nil {
			l.Error().Err(err).Msg("❌ CRITICAL: Agent.ProcessCallStart tüm denemelere rağmen başarısız oldu!")
			p.repo.UpdateSessionStatus(ctx, callID, "ERROR")
			// Çağrıyı asılı bırakmamak için Failsafe Hangup tetikliyoruz
			go func(rUrl, cid string) {
				conn, _ := amqp091.Dial(rUrl)
				if conn != nil {
					defer conn.Close()
					if ch, _ := conn.Channel(); ch != nil {
						defer ch.Close()
						payload := fmt.Sprintf(`{"callId":"%s","reason":"system_terminated"}`, cid)
						_ = ch.PublishWithContext(context.Background(), "sentiric_events", "call.terminate.request", false, false, amqp091.Publishing{
							ContentType: "application/json",
							Body:        []byte(payload),
						})
					}
				}
			}(p.rabbitURL, callID)
			return
		}

		p.repo.UpdateSessionStatus(ctx, callID, "HANDOVER_AGENT")
		l.Info().Str("call_id", callID).Msg("✅ Workflow görevini tamamladı. Kontrol Agent'ta.")
		return

	case "wait":
		durationSecs := 2
		if dsStr, ok := step.Params["duration_seconds"]; ok {
			if parsed, err := strconv.Atoi(dsStr); err == nil {
				durationSecs = parsed
			}
		}
		l.Info().Int("seconds", durationSecs).Msg("⏳ Workflow belirtilen süre kadar bekletiliyor...")
		time.Sleep(time.Duration(durationSecs) * time.Second)

	case "hangup":
		p.repo.UpdateSessionStatus(ctx, callID, "COMPLETED")
		l.Info().Str("call_id", callID).Msg("🛑 Workflow: Hangup komutu alındı. Sonlandırma sinyali gönderiliyor.")

		// [DÜZELTME]: amqp091 importu yukarıda olduğu için sorunsuz çalışır.
		go func(rUrl, cid string) {
			conn, err := amqp091.Dial(rUrl)
			if err == nil {
				defer conn.Close()
				if ch, err := conn.Channel(); err == nil {
					defer ch.Close()
					payload := fmt.Sprintf(`{"callId":"%s","reason":"workflow_hangup"}`, cid)
					_ = ch.PublishWithContext(context.Background(), "sentiric_events", "call.terminate.request", false, false, amqp091.Publishing{
						ContentType: "application/json",
						Body:        []byte(payload),
					})
				}
			}
		}(p.rabbitURL, callID)
		return
	}

	if step.Next != "" {
		p.executeStep(ctx, l, callID, traceID, rtpPort, rtpTarget, step.Next, wf, actionData)
	} else {
		p.repo.UpdateSessionStatus(ctx, callID, "COMPLETED")
		l.Info().Str("call_id", callID).Msg("✅ Workflow Tamamlandı.")
	}
}
