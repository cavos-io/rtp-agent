# Playground Integration Issues

## Status: In Progress

---

## Issue 1: Chat Transcript Tidak Muncul di Chat Panel

### Gejala
- Agent berbicara (audio terdengar ✅)
- Log `💬 [Transcript] Agent: "..."` muncul di server → artinya `PublishAgentTranscript` berhasil dipanggil ✅
- Tapi teks **tidak muncul** di chat panel LiveKit Playground ❌

### Root Cause (Temuan)
`@livekit/components-react >= 2.x` (dipakai Playground: `^2.9.20`) **tidak lagi menggunakan** `DataPacket_ChatMessage` (protobuf) untuk chat panel. Versi baru menggunakan **text stream** dengan topic `"lk.chat"` via `room.registerTextStreamHandler("lk.chat", ...)`.

LiveKit Cloud server version > 1.8.2 → otomatis pakai path text stream.

### Yang Sudah Dicoba

| Pendekatan | Hasil |
|---|---|
| `PublishDataPacket(&livekit.ChatMessage{...})` | ❌ Tidak muncul — protobuf path tidak dibaca `useChat` v2 |
| `SendText(JSON, {Topic: "lk.chat"})` | ❌ Tidak muncul |
| `SendText(plainText, {Topic: "lk.chat", StreamId: &id})` | ❌ Tidak muncul |

### Kode Saat Ini (agent_session.go)
```go
// PublishAgentTranscript
agtMsgID := fmt.Sprintf("agt-%d", now.UnixNano())
room.LocalParticipant.SendText(text, lksdk.StreamTextOptions{
    Topic:    "lk.chat",
    StreamId: &agtMsgID,
})
// + DataPacket_Transcription untuk subtitle overlay (tetap ada)
```

### Yang Belum Diketahui / Perlu Dicek

1. **Format payload yang tepat** — `setupChat` di `@livekit/components-core` mungkin mengharapkan JSON dengan struktur tertentu sebagai konten text stream. Perlu fetch source langsung:
   - `https://raw.githubusercontent.com/livekit/components-js/main/packages/core/src/components/chat.ts`
   - Cek apakah `registerTextStreamHandler` menerima plain text atau `JSON.stringify({id, message, timestamp})`

2. **Topic yang tepat** — Sudah dikonfirmasi `DataTopic.CHAT = "lk.chat"` dan `LegacyDataTopic.CHAT = "lk-chat-topic"`. Tapi belum dicoba topic alternatif.

3. **Filter participant kind** — Kemungkinan `setupChat` atau `useChat` memfilter dan hanya menampilkan pesan dari peserta `STANDARD` (bukan `AGENT`). Perlu cek source `useChat`.

4. **`SendText` di Go SDK v2.13.3** — Belum dikonfirmasi apakah format DataStream yang dikirim Go SDK kompatibel dengan format yang diharapkan JS SDK v2.18.

### Langkah Selanjutnya
1. Fetch source `setupChat` dari GitHub dan lihat EXACT handler untuk text stream (apa yang di-parse dari konten)
2. Coba format JSON: `{"id":"...","message":"...","timestamp":123}`
3. Coba tambah `Attributes` di `StreamTextOptions` (misal `generated: "true"`)
4. Cek apakah ada filter participant kind di `setupChat`
5. Alternatif: upgrade Go SDK ke v2.16.0 yang mungkin punya helper `SendChatMessage`

---

## Issue 2: Agent Audio Track Tidak Muncul di Playground UI

### Gejala
- Audio agent **terdengar** oleh user ✅
- Agent terdaftar sebagai `ParticipantKind.AGENT` ✅
- Atribut `lk.agent.state` di-set (listening/thinking/speaking/idle) ✅
- Tapi representasi visual agent di Playground UI **tidak muncul** ❌

### Root Cause (Dugaan)
`useVoiceAssistant` hook dari `@livekit/components-react` seharusnya mendeteksi participant dengan `kind === ParticipantKind.AGENT`. Kita sudah set ini via:

```go
// job.go
room, err := lksdk.ConnectToRoom(c.url, lksdk.ConnectInfo{
    ...
    ParticipantKind: lksdk.ParticipantAgent, // = ParticipantInfo_AGENT = 4
}, cb)
```

### Yang Belum Diketahui / Perlu Dicek

1. **Apakah `connect` mode meng-set ParticipantKind?** — Saat pakai `./agent connect <room>`, `ExecuteLocalJob` dipanggil tanpa `Connect()` dari `JobContext`. Perlu cek apakah `CLI connect` flow memanggil `jobCtx.Connect()` yang men-set `ParticipantKind: lksdk.ParticipantAgent`.

   Lihat `interface/cli/`:
   ```go
   // Kemungkinan flow connect tidak pakai ParticipantAgent
   // → agent join sebagai STANDARD participant
   // → useVoiceAssistant tidak mendeteksinya
   ```

2. **Atribut tambahan** — Playground mungkin butuh atribut selain `lk.agent.state`. Misal `lk.agent.name` atau `lk.publish-on-behalf-of` (harus tidak ada untuk agent utama).

3. **Timing** — Atribut `lk.agent.state` di-set setelah join. Jika Playground sudah render sebelum atribut diterima, mungkin tidak re-render (kemungkinan kecil karena LiveKit sync otomatis).

4. **Versi Playground** — `agents-playground.livekit.io` menggunakan managed agent protocol yang mungkin membutuhkan room metadata atau token claims tertentu.

### Langkah Selanjutnya

1. **Cek CLI connect flow** — Buka `interface/cli/` dan lihat bagaimana `connect` command memanggil `AgentServer`. Pastikan `lksdk.ConnectInfo.ParticipantKind = lksdk.ParticipantAgent` dipakai.

2. **Test dengan dispatch** (bukan `connect`) — Dispatch menggunakan `jobCtx.Connect()` yang sudah set `ParticipantAgent`. Kalau dengan dispatch agent muncul di UI tapi dengan `connect` tidak, maka ini masalah CLI connect flow.

3. **Tambah log** di connect — Print `room.LocalParticipant.Kind()` setelah join untuk konfirmasi.

4. **Cek source Playground** — `https://github.com/livekit/agents-playground` — lihat kondisi apa yang membuat agent section muncul.

---

## File Terkait

| File | Fungsi |
|---|---|
| `core/agent/agent_session.go` | `PublishUserTranscript`, `PublishAgentTranscript` |
| `core/agent/pipeline_agent.go` | `generateReply`, `sttLoop` |
| `interface/worker/job.go` | `Connect()` dengan `ParticipantKind: lksdk.ParticipantAgent` |
| `interface/worker/room_io.go` | Audio publish, track subscription |
| `interface/cli/` | `connect` command flow |

## SDK Versions
- Go: `github.com/livekit/server-sdk-go/v2 v2.13.3`
- Playground JS: `livekit-client ^2.18.0`, `@livekit/components-react ^2.9.20`
