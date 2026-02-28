package engine

import (
	"context"
	"encoding/json"
	"time"

	"github.com/redis/go-redis/v9"
	"github.com/rs/zerolog"
	"github.com/sentiric/sentiric-workflow-service/internal/client"
)

type Processor struct {
	redis   *redis.Client
	clients *client.GrpcClients
	log     zerolog.Logger
}

func NewProcessor(r *redis.Client, c *client.GrpcClients, l zerolog.Logger) *Processor {
	return &Processor{redis: r, clients: c, log: l}
}

// StartWorkflow: Bir çağrı başladığında (Event: call.started) çağrılır.
func (p *Processor) StartWorkflow(ctx context.Context, callID string, workflowDefJSON string) {
	var wf Workflow
	if err := json.Unmarshal([]byte(workflowDefJSON), &wf); err != nil {
		p.log.Error().Err(err).Str("call_id", callID).Msg("Workflow JSON parse hatası")
		return
	}

	p.log.Info().Str("call_id", callID).Str("wf_id", wf.ID).Msg("🚀 Workflow Başlatılıyor")

	// İlk adımı çalıştır
	p.executeStep(ctx, callID, wf.StartNode, &wf)
}

func (p *Processor) executeStep(ctx context.Context, callID, stepID string, wf *Workflow) {
	step, exists := wf.Steps[stepID]
	if !exists {
		p.log.Warn().Str("step", stepID).Msg("Step bulunamadı, akış durdu.")
		return
	}

	p.log.Debug().Str("call_id", callID).Str("step", stepID).Str("type", step.Type).Msg("Adım işleniyor...")

	// --- BASİT EYLEM MANTIĞI (MVP) ---
	switch step.Type {
	case "play_audio":
		// Media Service'e gRPC at
		// p.clients.Media.Play(...)

	case "execute_command":
		if step.Params["command"] == "media.enable_echo" {
			// p.clients.Media.EnableEcho(...)
			p.log.Info().Str("call_id", callID).Msg("🔊 Echo Modu Aktif Edildi (Workflow Emri)")
		}

	case "handover_to_agent":
		// p.clients.Agent.StartConversation(...)
		p.log.Info().Str("call_id", callID).Msg("🤖 Çağrı Agent Servisine devredildi.")
		return // Akış kontrolü Agent'a geçti.

	case "wait":
		// Asenkron bekleme (Basit sleep, prod için timer service gerekir)
		time.Sleep(2 * time.Second)
	}

	// Sonraki adım varsa recursive çağır
	if step.Next != "" {
		p.executeStep(ctx, callID, step.Next, wf)
	} else {
		p.log.Info().Str("call_id", callID).Msg("✅ Workflow Tamamlandı.")
	}
}
