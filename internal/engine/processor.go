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
	"google.golang.org/grpc/metadata"
)

type Processor struct {
	redis   *redis.Client
	clients *client.GrpcClients
	log     zerolog.Logger
}

func NewProcessor(r *redis.Client, c *client.GrpcClients, l zerolog.Logger) *Processor {
	return &Processor{redis: r, clients: c, log: l}
}

// Helpers for Protobuf
func toUint32Ptr(v uint32) *uint32 { return &v }
func toStringPtr(v string) *string { return &v }

func (p *Processor) StartWorkflow(ctx context.Context, callID, traceID string, rtpPort uint32, rtpTarget string, workflowDefJSON string, actionData map[string]string) {
	var wf Workflow
	if err := json.Unmarshal([]byte(workflowDefJSON), &wf); err != nil {
		p.log.Error().Err(err).Str("call_id", callID).Msg("Workflow JSON parse hatası")
		return
	}

	l := p.log.With().Str("trace_id", traceID).Str("call_id", callID).Logger()
	l.Info().Str("wf_id", wf.ID).Msg("🚀 Workflow Başlatılıyor")

	p.executeStep(ctx, l, callID, traceID, rtpPort, rtpTarget, wf.StartNode, &wf, actionData)
}

func (p *Processor) executeStep(ctx context.Context, l zerolog.Logger, callID, traceID string, rtpPort uint32, rtpTarget string, stepID string, wf *Workflow, actionData map[string]string) {
	step, exists := wf.Steps[stepID]
	if !exists {
		l.Warn().Str("step", stepID).Msg("Step bulunamadı, akış durdu.")
		return
	}

	outCtx := metadata.AppendToOutgoingContext(context.Background(), "x-trace-id", traceID)

	l.Debug().Str("step", stepID).Str("type", step.Type).Msg("Adım işleniyor...")

	// [NO-HARDCODE]: "record" parametresini artık dinamik olarak kontrol ediyoruz
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
		}
		delete(actionData, "record")
	}

	switch step.Type {
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

		// Dialplan ID'yi ve diğer parametreleri actionData'dan dinamik olarak al
		dialplanID, _ := actionData["dialplanId"] // default ""
		if dialplanID == "" {
			dialplanID = "DP_DEMO_AI_ASSISTANT" // Fallback
		}

		_, err := p.clients.Agent.ProcessCallStart(outCtx, &agentv1.ProcessCallStartRequest{
			CallId:     callID,
			DialplanId: dialplanID,
		})

		if err != nil {
			l.Error().Err(err).Msg("Agent.ProcessCallStart RPC çağrısı başarısız oldu.")
		}

		l.Info().Str("call_id", callID).Msg("✅ Workflow görevini tamamladı. Kontrol Agent'ta.")
		return

	case "wait":
		// TODO: Redis tabanlı asenkron timer
		time.Sleep(2 * time.Second)
	}

	// Sonraki adım varsa geç
	if step.Next != "" {
		p.executeStep(ctx, l, callID, traceID, rtpPort, rtpTarget, step.Next, wf, actionData)
	} else {
		l.Info().Str("call_id", callID).Msg("✅ Workflow Tamamlandı.")
	}
}
