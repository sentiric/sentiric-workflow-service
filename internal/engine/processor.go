// sentiric-workflow-service/internal/engine/processor.go
package engine

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"
	"github.com/rs/zerolog"
	agentv1 "github.com/sentiric/sentiric-contracts/gen/go/sentiric/agent/v1"
	mediav1 "github.com/sentiric/sentiric-contracts/gen/go/sentiric/media/v1"
	"github.com/sentiric/sentiric-workflow-service/internal/client"
	"google.golang.org/grpc/metadata"
)

type Processor struct {
	redis   *redis.Client
	db      *pgxpool.Pool
	clients *client.GrpcClients
	log     zerolog.Logger
}

func NewProcessor(r *redis.Client, db *pgxpool.Pool, c *client.GrpcClients, l zerolog.Logger) *Processor {
	return &Processor{redis: r, db: db, clients: c, log: l}
}

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

	// [KRİTİK DÜZELTME]: Foreign Key hatasını önlemek için gelen JSON'u otomatik olarak workflows tablosuna Upsert yapıyoruz.
	upsertQuery := `
		INSERT INTO workflows (id, tenant_id, name, definition) 
		VALUES ($1, 'system', $1, $2::jsonb) 
		ON CONFLICT (id) DO UPDATE SET definition = EXCLUDED.definition`
	_, err := p.db.Exec(context.Background(), upsertQuery, wf.ID, workflowDefJSON)
	if err != nil {
		l.Warn().Err(err).Msg("Workflow tanımı DB'ye kaydedilemedi, Foreign Key hatası alınabilir.")
	}

	// Artık güvenle Session oluşturabiliriz
	sessionQuery := `
		INSERT INTO workflow_sessions (call_id, workflow_id, current_step_id, status) 
		VALUES ($1, $2, $3, 'RUNNING')
		ON CONFLICT (session_id) DO NOTHING`
	_, _ = p.db.Exec(context.Background(), sessionQuery, callID, wf.ID, wf.StartNode)

	p.executeStep(ctx, l, callID, traceID, rtpPort, rtpTarget, wf.StartNode, &wf, actionData)
}

func (p *Processor) executeStep(ctx context.Context, l zerolog.Logger, callID, traceID string, rtpPort uint32, rtpTarget string, stepID string, wf *Workflow, actionData map[string]string) {
	step, exists := wf.Steps[stepID]
	if !exists {
		l.Warn().Str("step", stepID).Msg("Step bulunamadı, akış durdu.")
		_, _ = p.db.Exec(context.Background(), `UPDATE workflow_sessions SET status = 'ERROR', updated_at = NOW() WHERE call_id = $1`, callID)
		return
	}

	_, _ = p.db.Exec(context.Background(), `UPDATE workflow_sessions SET current_step_id = $1, updated_at = NOW() WHERE call_id = $2`, stepID, callID)

	outCtx := metadata.AppendToOutgoingContext(context.Background(), "x-trace-id", traceID)
	l.Debug().Str("step", stepID).Str("type", step.Type).Msg("Adım işleniyor...")

	if record, ok := actionData["record"]; ok && record == "true" {
		l.Info().Msg("🎤 Kayıt talimatı algılandı. Media Service'e StartRecording komutu gönderiliyor...")
		_, err := p.clients.Media.StartRecording(outCtx, &mediav1.StartRecordingRequest{
			CallId:        callID,
			TraceId:       traceID,
			ServerRtpPort: rtpPort,
			OutputUri:     fmt.Sprintf("s3://sentiric/recordings/%s.wav", callID),
			SampleRate:    toUint32Ptr(16000), // [FIX]: 16kHz olarak güncellendi
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

		_, _ = p.db.Exec(context.Background(), `UPDATE workflow_sessions SET status = 'HANDOVER_AGENT', updated_at = NOW() WHERE call_id = $1`, callID)
		l.Info().Str("call_id", callID).Msg("✅ Workflow görevini tamamladı. Kontrol Agent'ta.")
		return

	case "wait":
		time.Sleep(2 * time.Second)
	case "hangup":
		_, _ = p.db.Exec(context.Background(), `UPDATE workflow_sessions SET status = 'COMPLETED', updated_at = NOW() WHERE call_id = $1`, callID)
		return
	}

	if step.Next != "" {
		p.executeStep(ctx, l, callID, traceID, rtpPort, rtpTarget, step.Next, wf, actionData)
	} else {
		_, _ = p.db.Exec(context.Background(), `UPDATE workflow_sessions SET status = 'COMPLETED', updated_at = NOW() WHERE call_id = $1`, callID)
		l.Info().Str("call_id", callID).Msg("✅ Workflow Tamamlandı.")
	}
}
