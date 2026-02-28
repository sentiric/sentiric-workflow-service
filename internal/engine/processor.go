// sentiric-workflow-service/internal/engine/processor.go
package engine

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
	"github.com/rs/zerolog"
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

func (p *Processor) StartWorkflow(ctx context.Context, callID string, rtpPort uint32, rtpTarget string, workflowDefJSON string) {
	var wf Workflow
	if err := json.Unmarshal([]byte(workflowDefJSON), &wf); err != nil {
		p.log.Error().Err(err).Str("call_id", callID).Msg("Workflow JSON parse hatası")
		return
	}

	p.log.Info().Str("call_id", callID).Str("wf_id", wf.ID).Msg("🚀 Workflow Başlatılıyor")

	p.executeStep(ctx, callID, rtpPort, rtpTarget, wf.StartNode, &wf)
}

func (p *Processor) executeStep(ctx context.Context, callID string, rtpPort uint32, rtpTarget string, stepID string, wf *Workflow) {
	step, exists := wf.Steps[stepID]
	if !exists {
		p.log.Warn().Str("step", stepID).Msg("Step bulunamadı, akış durdu.")
		return
	}

	p.log.Debug().Str("call_id", callID).Str("step", stepID).Str("type", step.Type).Msg("Adım işleniyor...")

	// Trace ID ekleyelim (Media service logları için şart)
	outCtx := metadata.AppendToOutgoingContext(context.Background(), "x-trace-id", callID)

	switch step.Type {
	case "execute_command":
		if step.Params["command"] == "media.enable_echo" {
			p.log.Info().Str("call_id", callID).Msg("🔊 Workflow: Echo ve Kayıt komutları gönderiliyor...")

			// 1. Kaydı Başlat
			_, err := p.clients.Media.StartRecording(outCtx, &mediav1.StartRecordingRequest{
				CallId:        callID,
				TraceId:       callID,
				ServerRtpPort: rtpPort,
				OutputUri:     fmt.Sprintf("s3://sentiric/recordings/%s.wav", callID),
				SampleRate:    toUint32Ptr(8000),
				Format:        toStringPtr("wav"),
			})
			if err != nil {
				p.log.Error().Err(err).Msg("Media.StartRecording failed")
			}

			// 2. Nat Warmer Play
			_, err = p.clients.Media.PlayAudio(outCtx, &mediav1.PlayAudioRequest{
				AudioUri:      "file://audio/tr/system/nat_warmer.wav",
				ServerRtpPort: rtpPort,
				RtpTargetAddr: rtpTarget,
			})

			// 3. Enable Echo Test
			_, err = p.clients.Media.PlayAudio(outCtx, &mediav1.PlayAudioRequest{
				AudioUri:      "control://enable_echo",
				ServerRtpPort: rtpPort,
				RtpTargetAddr: rtpTarget,
			})
		}

	case "handover_to_agent":
		p.log.Info().Str("call_id", callID).Msg("🤖 Workflow: Çağrı Agent Servisine devrediliyor...")
		return // Akış kontrolü bitti

	case "wait":
		// TODO: Redis tabanlı asenkron timer mekanizması gelecek
		time.Sleep(2 * time.Second)
	}

	// Sonraki adım varsa geç
	if step.Next != "" {
		p.executeStep(ctx, callID, rtpPort, rtpTarget, step.Next, wf)
	} else {
		p.log.Info().Str("call_id", callID).Msg("✅ Workflow Tamamlandı.")
	}
}
