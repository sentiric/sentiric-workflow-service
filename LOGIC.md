# 🧬 Workflow SAGA & State Machine Logic
1. **State Store (Redis):** Çağrı durumu geçicidir, asla Postgres'e canlı durum yazılmaz.
2. **Audit Logging:** Çağrı bittiğinde (Terminal State), son durum özeti `workflow_execution_logs` tablosuna "tek seferlik" (Idempotent) olarak arşivlenir.
3. **Dynamic Routing:** `dialplan-service`'den gelen aksiyon verisi içindeki `workflow_id`'yi kullanarak dinamik JSON yükler.
