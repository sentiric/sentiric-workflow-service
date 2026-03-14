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

// GetWorkflowDefinition: Veritabanından (Postgres) iş akış şemasını getirir.
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
// [FIX]: Build hatasını önlemek için bu fonksiyon buraya geri eklendi.
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

// --- OTURUM YÖNETİMİ (REDIS TABANLI) ---

// CreateSession: Yeni bir çağrı oturumu oluşturur. Veriyi JSON string olarak saklar.
func (r *WorkflowRepository) CreateSession(ctx context.Context, callID, workflowID, startNode string) error {
	key := fmt.Sprintf("wf_session:%s", callID)
	sessionData := map[string]interface{}{
		"call_id":      callID,
		"workflow_id":  workflowID,
		"current_step": startNode,
		"status":       "RUNNING",
		"updated_at":   time.Now().Format(time.RFC3339),
	}

	jsonData, err := json.Marshal(sessionData)
	if err != nil {
		return err
	}

	// 1 saatlik TTL ile Redis'e kaydet
	return r.redis.Set(ctx, key, jsonData, time.Hour).Err()
}

// UpdateSessionStep: Oturumun mevcut adımını günceller.
func (r *WorkflowRepository) UpdateSessionStep(ctx context.Context, callID, stepID string) {
	r.updateSessionField(ctx, callID, "current_step", stepID)
}

// UpdateSessionStatus: Oturumun durumunu günceller.
func (r *WorkflowRepository) UpdateSessionStatus(ctx context.Context, callID, status string) {
	r.updateSessionField(ctx, callID, "status", status)

	// Eğer çağrı bittiyse TTL'i 5 dakikaya düşür (Clean-up)
	if status == "COMPLETED" || status == "ERROR" {
		r.redis.Expire(ctx, fmt.Sprintf("wf_session:%s", callID), 5*time.Minute)
	}
}

// updateSessionField: Redis'teki JSON objesini güvenli şekilde güncelleyen yardımcı metod.
func (r *WorkflowRepository) updateSessionField(ctx context.Context, callID, field, value string) {
	key := fmt.Sprintf("wf_session:%s", callID)

	// 1. Mevcut veriyi çek
	val, err := r.redis.Get(ctx, key).Result()
	if err != nil {
		return // Session bulunamadıysa (timeout vb.) işlem yapma
	}

	// 2. Unmarshal et
	var sessionData map[string]interface{}
	if err := json.Unmarshal([]byte(val), &sessionData); err != nil {
		return
	}

	// 3. Güncelle
	sessionData[field] = value
	sessionData["updated_at"] = time.Now().Format(time.RFC3339)

	// 4. Tekrar kaydet (Marshal)
	updatedJson, _ := json.Marshal(sessionData)
	r.redis.Set(ctx, key, updatedJson, time.Hour)
}

// UpsertWorkflow: Çalışma anında (runtime) workflow tanımı kaydetmek için (Postgres).
func (r *WorkflowRepository) UpsertWorkflow(ctx context.Context, id, name, definition string) error {
	query := `
		INSERT INTO workflows (id, tenant_id, name, definition) 
		VALUES ($1, 'system', $2, $3::jsonb) 
		ON CONFLICT (id) DO UPDATE SET definition = EXCLUDED.definition`
	_, err := r.db.Exec(ctx, query, id, name, definition)
	return err
}
