# 💠 Sentiric Workflow Service

[![Status](https://img.shields.io/badge/status-active-success.svg)]()
[![Layer](https://img.shields.io/badge/layer-Logic%20(L2)-blue.svg)]()

**Sentiric Workflow Service**, platformun "Programlanabilir Beyni"dir. Telekomünikasyon katmanından gelen olayları (`call.started`, `dtmf.received`) dinler ve veritabanında tanımlı **JSON İş Akışlarına** göre diğer servislere emir verir.

## 🎯 Temel Sorumluluklar

1.  **Orkestrasyon:** Çağrının yaşam döngüsünü yönetir. B2BUA, Media ve Agent servisleri arasındaki trafik polisidir.
2.  **State Machine:** Her çağrının hangi adımda olduğunu (Örn: "Ana Menüde", "Agent Bekliyor") Redis üzerinde takip eder.
3.  **Dinamik Karar:** Kod değişikliği gerektirmeden, veritabanındaki JSON kurallarını değiştirerek çağrı akışını değiştirir.

## 🔗 Servis İlişkileri

*   **B2BUA Service:** Workflow'a "Çağrı Başladı" olayını gönderir. Workflow, B2BUA'ya "Transfer Et" veya "Kapat" emri verir.
*   **Media Service:** Workflow, Media'ya "Dosya Çal", "Kayıt Başlat", "Echo Moduna Geç" emri verir.
*   **Agent Service:** Workflow, "Kullanıcıyla Konuş" (AI Handover) emrini Agent'a verir.

## 🛠️ Teknoloji Yığını

*   **Dil:** Go
*   **State Store:** Redis (Ephemeral Session Data)
*   **Rule Store:** PostgreSQL (`workflows` tablosu JSONB)
*   **Event Bus:** RabbitMQ
*   **Communication:** gRPC (Command/Control)

---
## 🏛️ Anayasal Konum

Bu servis, [Sentiric Anayasası'nın](https://github.com/sentiric/sentiric-governance) **Core Logic Layer**'ında yer alır ve "Code-over-Configuration" prensibini "Configuration-over-Code"a dönüştürür.