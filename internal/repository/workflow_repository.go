// File: internal/repository/workflow_repository.go
package repository

import (
	"context"
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
		r.log.Error().Err(err).Msg("❌ Redis HSet error during CreateSession")
		return err
	}

	// 1 Saatlik TTL
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
	r.redis.HSet(ctx, key, map[string]interface{}{
		"status":     status,
		"updated_at": time.Now().Format(time.RFC3339),
	})

	// Eğer tamamlandıysa hemen RAM'i temizle (5 dakika sonra düşür)
	if status == "COMPLETED" || status == "ERROR" {
		r.redis.Expire(ctx, key, 5*time.Minute)
	}
}

// Yeni: Asenkron olaylar için oturum verisini çekme
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
