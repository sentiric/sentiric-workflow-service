# 🧠 Workflow Service - Mantık Mimarisi

**Rol:** The Cortex (Karar Verici Üst Katman).

## 1. Çalışma Prensibi (The Engine)

Servis, **Olay Güdümlü (Event-Driven)** çalışır. Kendi başına bir şey yapmaz, olaylara tepki verir.

### Akış Örneği: Echo Testi

1.  **Giriş:** `b2bua` -> `RabbitMQ` -> `call.started` (Dest: 9999).
2.  **Karar:** `workflow-service` veritabanından `9999` için tanımlı akışı çeker: `wf_system_echo`.
3.  **Adım 1:** JSON: `{"type": "play", "file": "welcome.wav"}`
    *   Eylem: `media-service.PlayAudio(...)` gRPC çağrısı.
4.  **Adım 2:** JSON: `{"type": "execute_command", "command": "media.enable_echo"}`
    *   Eylem: `media-service`'e Echo komutu.
5.  **Adım 3:** JSON: `{"type": "wait", "seconds": 60}`
    *   Eylem: 60 saniye boyunca `sleep`.

## 2. Agent Service ile Farkı

| Özellik | Workflow Service | Agent Service |
| :--- | :--- | :--- |
| **Metafor** | Yönetmen | Oyuncu |
| **Görevi** | Sahneyi kurar, oyuncuyu çağırır. | Sahneye çıkar, diyaloğu gerçekleştirir. |
| **Yetenek** | Akış kontrolü, bekleme, yönlendirme. | STT/TTS/LLM koordinasyonu, konuşma. |

**Kural:** Agent Service asla kendi başına "Ben şimdi ne yapayım?" diye karar vermez. Workflow ona "Sahneye Çık" diyene kadar bekler.