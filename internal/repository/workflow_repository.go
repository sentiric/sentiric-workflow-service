// Dosya: sentiric-workflow-service/internal/repository/workflow_repository.go
package repository

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"
	"github.com/rs/zerolog"
)

type WorkflowRepository struct {
	db    *pgxpool.Pool
	redis *redis.Client
	log   zerolog.Logger
}

func NewWorkflowRepository(db *pgxpool.Pool, rds *redis.Client, log zerolog.Logger) *WorkflowRepository {
	return &WorkflowRepository{db: db, redis: rds, log: log}
}

func (r *WorkflowRepository) GetWorkflowDefinition(ctx context.Context, workflowID string) (string, error) {
	var definition []byte
	query := `SELECT definition FROM workflows WHERE id = $1 AND is_active = TRUE`

	err := r.db.QueryRow(ctx, query, workflowID).Scan(&definition)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return "", fmt.Errorf("workflow not found: %s", workflowID)
		}
		return "", err
	}
	return string(definition), nil
}

func MapActionToWorkflowID(actionType string) string {
	switch actionType {
	case "ECHO_TEST", "ACTION_TYPE_ECHO_TEST":
		return "wf_system_echo"
	case "START_AI_CONVERSATION", "ACTION_TYPE_START_AI_CONVERSATION":
		return "wf_demo_ai"
	default:
		return ""
	}
}

// --- ATOMIC STATE MANAGEMENT (REDIS HASH) ---

func (r *WorkflowRepository) CreateSession(ctx context.Context, callID, workflowID, startNode, traceID string, rtpPort uint32, rtpTarget string) error {
	key := fmt.Sprintf("wf_session:%s", callID)

	// HSet ile Race-Condition riski olmadan Atomik yazma
	err := r.redis.HSet(ctx, key, map[string]interface{}{
		"call_id":      callID,
		"workflow_id":  workflowID,
		"current_step": startNode,
		"status":       "RUNNING",
		"trace_id":     traceID,
		"rtp_port":     rtpPort,
		"rtp_target":   rtpTarget,
		"updated_at":   time.Now().Format(time.RFC3339),
	}).Err()

	if err != nil {
		// [ARCH-COMPLIANCE] ARCH-007
		r.log.Error().Str("event", "REDIS_HSET_ERROR").Err(err).Msg("❌ Redis HSet error during CreateSession")
		return err
	}

	return r.redis.Expire(ctx, key, time.Hour).Err()
}

func (r *WorkflowRepository) UpdateSessionStep(ctx context.Context, callID, stepID string) {
	key := fmt.Sprintf("wf_session:%s", callID)
	r.redis.HSet(ctx, key, map[string]interface{}{
		"current_step": stepID,
		"updated_at":   time.Now().Format(time.RFC3339),
	})
}

func (r *WorkflowRepository) UpdateSessionStatus(ctx context.Context, callID, status string) {
	key := fmt.Sprintf("wf_session:%s", callID)

	// 1. Redis durumunu her zaman güncelle (Real-time izleme için)
	r.redis.HSet(ctx, key, "status", status, "updated_at", time.Now().Format(time.RFC3339))

	// 2. Terminal durum kontrolü (COMPLETED, ERROR, HANDOVER)
	if status == "COMPLETED" || status == "ERROR" || status == "HANDOVER_AGENT" {

		// [SRE IDEMPOTENCY LATCH]: Sadece bir kez PostgreSQL'e yazılmasını garanti et.
		// 'is_archived' alanını sadece yoksa 'true' yapar.
		isFirstTime, _ := r.redis.HSetNX(ctx, key, "is_archived", "true").Result()

		if isFirstTime {
			// Yarışı bu thread kazandı, DB insert işlemini yapabilir.
			sessionData, err := r.GetSession(ctx, callID)
			if err == nil && len(sessionData) > 0 {
				wfID := sessionData["workflow_id"]
				stateJSON, _ := json.Marshal(sessionData)

				dbStatus := status
				if status == "ERROR" {
					dbStatus = "FAILED"
				}

				query := `
					INSERT INTO workflow_execution_logs (call_id, workflow_id, status, final_state_data) 
					VALUES ($1, $2, $3, $4::jsonb)
				`
				_, dbErr := r.db.Exec(ctx, query, callID, wfID, dbStatus, stateJSON)

				if dbErr != nil {
					r.log.Error().Str("event", "DB_WRITE_FAIL").Err(dbErr).Str("call_id", callID).Msg("❌ DB Write Fail")
				} else {
					r.log.Info().Str("event", "AUDIT_LOG_ARCHIVED").Str("call_id", callID).Msg("💾 Workflow Audit Log archived (First & Only trigger).")
				}

			}
			// Bellek temizliği için TTL'i düşür
			r.redis.Expire(ctx, key, 5*time.Minute)
		} else {
			r.log.Debug().Str("event", "SESSION_ALREADY_ARCHIVED").Str("call_id", callID).Msg("⏭️ Session already archived. Skipping duplicate DB insert.")
		}

	}
}

func (r *WorkflowRepository) GetSession(ctx context.Context, callID string) (map[string]string, error) {
	key := fmt.Sprintf("wf_session:%s", callID)
	session, err := r.redis.HGetAll(ctx, key).Result()
	if err != nil {
		return nil, err
	}
	if len(session) == 0 {
		return nil, redis.Nil
	}
	return session, nil
}

func (r *WorkflowRepository) UpsertWorkflow(ctx context.Context, id, name, definition string) error {
	query := `
		INSERT INTO workflows (id, tenant_id, name, definition) 
		VALUES ($1, 'system', $2, $3::jsonb) 
		ON CONFLICT (id) DO UPDATE SET definition = EXCLUDED.definition`
	_, err := r.db.Exec(ctx, query, id, name, definition)
	return err
}

// GetAnnouncementPath, belirli bir dilde ve tenant'ta anons dosyasının yolunu DB'den çeker.
func (r *WorkflowRepository) GetAnnouncementPath(ctx context.Context, annID, tenantID, langCode string) (string, error) {
	var audioPath string
	// Spesifik tenant'ı arar, bulamazsa 'system' tenant'ına (fallback) düşer.
	query := `
        SELECT audio_path FROM announcements
        WHERE id = $1 AND language_code = $2 AND (tenant_id = $3 OR tenant_id = 'system')
        ORDER BY tenant_id DESC LIMIT 1`

	err := r.db.QueryRow(ctx, query, annID, langCode, tenantID).Scan(&audioPath)
	if err != nil {
		r.log.Error().Str("event", "ANNOUNCEMENT_NOT_FOUND").Err(err).Str("ann_id", annID).Str("lang", langCode).Msg("Anons veritabanında bulunamadı!")
		return "", err
	}
	return audioPath, nil
}
