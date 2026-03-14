# 📋 Mimari Uyum Görevi: State Management Migration

**Durum:** 🛠️ Devam Ediyor  
**Gerekçe:** [Sentiric Spec ARCH-003] Gerçek zamanlı oturum yönetimi Redis'e taşınmalıdır.

## Yapılacaklar
- [x] `workflow_repository.go` içindeki Session metodlarını Redis'e taşı.
- [x] PostgreSQL sadece "Workflow Definitions" (Statik Kurallar) için kullanılmalı.
- [x] Oturum verileri için 1 saatlik TTL (Time-To-Live) ekle.
- [x] `processor.go` akışını Redis tabanlı oturum yönetimine güncelle.

**Not:** Bu değişiklik sistemin gerçek zamanlı tepki hızını artıracak ve DB üzerindeki yükü azaltacaktır.