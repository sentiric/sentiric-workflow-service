package repository

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/rs/zerolog"
)

type WorkflowRepository struct {
	db  *pgxpool.Pool
	log zerolog.Logger
}

func NewWorkflowRepository(db *pgxpool.Pool, log zerolog.Logger) *WorkflowRepository {
	return &WorkflowRepository{db: db, log: log}
}

// GetWorkflowDefinition: Belirli bir ID'ye sahip akışın JSON tanımını getirir.
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

// MapActionToWorkflowID: Aksiyon tipine göre varsayılan workflow ID'sini döndürür.
// Bu mapping, config tablosunda tutulabilir ama şimdilik kodda stratejik olarak tutuyoruz.
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

// UpsertWorkflow: Processor tarafından çağrılan, çalışma anında JSON kaydetme metodu (Eskiden processor içindeydi).
func (r *WorkflowRepository) UpsertWorkflow(ctx context.Context, id, name, definition string) error {
	query := `
		INSERT INTO workflows (id, tenant_id, name, definition) 
		VALUES ($1, 'system', $2, $3::jsonb) 
		ON CONFLICT (id) DO UPDATE SET definition = EXCLUDED.definition`
	_, err := r.db.Exec(ctx, query, id, name, definition)
	return err
}

func (r *WorkflowRepository) CreateSession(ctx context.Context, callID, workflowID, startNode string) error {
	query := `
		INSERT INTO workflow_sessions (call_id, workflow_id, current_step_id, status) 
		VALUES ($1, $2, $3, 'RUNNING')
		ON CONFLICT (session_id) DO NOTHING` // call_id unique olmayabilir, ID UUID'dir.

	// Basitlik için call_id index'ine güveniyoruz.
	_, err := r.db.Exec(ctx, query, callID, workflowID, startNode)
	return err
}

func (r *WorkflowRepository) UpdateSessionStep(ctx context.Context, callID, stepID string) {
	_, _ = r.db.Exec(ctx, `UPDATE workflow_sessions SET current_step_id = $1, updated_at = NOW() WHERE call_id = $2`, stepID, callID)
}

func (r *WorkflowRepository) UpdateSessionStatus(ctx context.Context, callID, status string) {
	_, _ = r.db.Exec(ctx, `UPDATE workflow_sessions SET status = $1, updated_at = NOW() WHERE call_id = $2`, status, callID)
}
