// sentiric-workflow-service/internal/engine/processor.go
package engine

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
	"github.com/rs/zerolog"
	agentv1 "github.com/sentiric/sentiric-contracts/gen/go/sentiric/agent/v1"
	mediav1 "github.com/sentiric/sentiric-contracts/gen/go/sentiric/media/v1"
	"github.com/sentiric/sentiric-workflow-service/internal/client"
	"github.com/sentiric/sentiric-workflow-service/internal/repository" // YENİ
	"google.golang.org/grpc/metadata"
)

type Processor struct {
	redis   *redis.Client
	repo    *repository.WorkflowRepository // YENİ: Repository eklendi
	clients *client.GrpcClients
	log     zerolog.Logger
}

// NewProcessor constructor güncellendi
func NewProcessor(r *redis.Client, repo *repository.WorkflowRepository, c *client.GrpcClients, l zerolog.Logger) *Processor {
	return &Processor{redis: r, repo: repo, clients: c, log: l}
}

func toUint32Ptr(v uint32) *uint32 { return &v }
func toStringPtr(v string) *string { return &v }

func (p *Processor) StartWorkflow(ctx context.Context, callID, traceID string, rtpPort uint32, rtpTarget string, workflowDefJSON string, actionData map[string]string) {
	var wf Workflow
	// [RESILIENCE UPDATE]: JSON hatası yakalandığında detaylı log bas.
	if err := json.Unmarshal([]byte(workflowDefJSON), &wf); err != nil {
		p.log.Error().
			Err(err).
			Str("call_id", callID).
			Str("json_snippet", workflowDefJSON). // Hatalı JSON'u loga bas
			Msg("❌ CRITICAL: Workflow JSON parse hatası! Akış başlatılamıyor.")

		// Opsiyonel: Burada "Acil Durum Anonsu" çaldırılabilir veya çağrı sonlandırılabilir.
		// p.clients.B2BUA.TerminateCall(...)
		return
	}

	l := p.log.With().Str("trace_id", traceID).Str("call_id", callID).Logger()
	l.Info().Str("wf_id", wf.ID).Msg("🚀 Workflow Başlatılıyor (DB Backed)")

	// 1. Session Oluştur (Repo üzerinden)
	_ = p.repo.CreateSession(ctx, callID, wf.ID, wf.StartNode)

	p.executeStep(ctx, l, callID, traceID, rtpPort, rtpTarget, wf.StartNode, &wf, actionData)
}

func (p *Processor) executeStep(ctx context.Context, l zerolog.Logger, callID, traceID string, rtpPort uint32, rtpTarget string, stepID string, wf *Workflow, actionData map[string]string) {
	step, exists := wf.Steps[stepID]
	if !exists {
		l.Warn().Str("step", stepID).Msg("Step bulunamadı, akış durdu.")
		p.repo.UpdateSessionStatus(ctx, callID, "ERROR")
		return
	}

	p.repo.UpdateSessionStep(ctx, callID, stepID)

	outCtx := metadata.AppendToOutgoingContext(context.Background(), "x-trace-id", traceID)
	l.Debug().Str("step", stepID).Str("type", step.Type).Msg("Adım işleniyor...")

	// Global Record Logic (Eğer actionData'da varsa, akışın başında başlat)
	// Not: Bu logic normalde bir "Node" olmalı ama geriye dönük uyum için burada tutuyoruz.
	if record, ok := actionData["record"]; ok && record == "true" {
		l.Info().Msg("🎤 Kayıt talimatı algılandı. Media Service'e StartRecording komutu gönderiliyor...")
		_, err := p.clients.Media.StartRecording(outCtx, &mediav1.StartRecordingRequest{
			CallId:        callID,
			TraceId:       traceID,
			ServerRtpPort: rtpPort,
			OutputUri:     fmt.Sprintf("s3://sentiric/recordings/%s.wav", callID),
			SampleRate:    toUint32Ptr(8000), // [FIX]: Telekom standardı 8kHz
			Format:        toStringPtr("wav"),
		})
		if err != nil {
			l.Error().Err(err).Msg("Media.StartRecording RPC çağrısı başarısız oldu.")
		} else {
			l.Info().Msg("✅ Kayıt başarıyla başlatıldı.")
		}
		delete(actionData, "record") // Tekrar tetiklenmesin
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
				l.Error().Err(err).Msg("Media.PlayAudio RPC çağrısı başarısız oldu.")
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
		_, err := p.clients.Agent.ProcessCallStart(outCtx, &agentv1.ProcessCallStartRequest{
			CallId:     callID,
			DialplanId: dialplanID,
		})
		if err != nil {
			l.Error().Err(err).Msg("Agent.ProcessCallStart RPC çağrısı başarısız oldu.")
		}

		p.repo.UpdateSessionStatus(ctx, callID, "HANDOVER_AGENT")
		l.Info().Str("call_id", callID).Msg("✅ Workflow görevini tamamladı. Kontrol Agent'ta.")
		return

	case "wait":
		time.Sleep(2 * time.Second)
	case "hangup":
		p.repo.UpdateSessionStatus(ctx, callID, "COMPLETED")
		// Burada B2BUA.TerminateCall çağrılabilir ama akış bitince B2BUA zaten zaman aşımına uğrayabilir
		// Veya explicit terminate eklenebilir.
		return
	}

	if step.Next != "" {
		p.executeStep(ctx, l, callID, traceID, rtpPort, rtpTarget, step.Next, wf, actionData)
	} else {
		p.repo.UpdateSessionStatus(ctx, callID, "COMPLETED")
		l.Info().Str("call_id", callID).Msg("✅ Workflow Tamamlandı.")
	}
}
