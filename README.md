# assemblyai-whisper-bridge

An OpenAI-compatible Whisper transcription endpoint, backed by AssemblyAI.

Put it in front of any client that speaks the OpenAI Whisper API and it will
transcribe through AssemblyAI instead. The first consumer is
[Voxtype](https://voxtype.io), the push-to-talk dictation daemon bundled with
[Omarchy](https://omarchy.org), but nothing in the design is Voxtype-specific.

> **Status: working local bridge.** MVP-1 is implemented and tested (`main.go`, `main_test.go`: 12 tests passing). The design brief below is kept intact as the record of the research that motivated it.

## Local operation

```sh
go build -o assemblyai-whisper-bridge .
cp .env.example .env  # then fill in ASSEMBLYAI_API_KEY
./assemblyai-whisper-bridge
go test ./...
```

A project-local `.env` file (simple `KEY=value` lines, `#` comments and blank
lines allowed) is loaded on startup from the process working directory when
present. Variables already set in the process environment take precedence,
including an explicitly empty value; a missing `.env` is ignored and any
malformed non-comment line fails startup with an error. `.env` is
gitignored; `.env.example` shows the safe placeholders. Protect the
project-local credential file with `chmod 600 .env`.

Point Voxtype at `http://127.0.0.1:8787` (see `remote_endpoint` in section 3).

### Configuration

| Variable | Default | Purpose |
|---|---|---|
| `ADDR` | `127.0.0.1:8787` | Listen address |
| `ASSEMBLYAI_API_KEY` | _(empty)_ | Sent verbatim as the upstream `Authorization` header; required for real transcription |
| `ASSEMBLYAI_MODEL` | `universal-3-5-pro` | Sent as the upstream `X-AAI-Model` header |
| `ASSEMBLYAI_URL` | `https://sync.assemblyai.com/transcribe` | Upstream endpoint (override for tests) |

### systemd user service example

Keep the key in a separate untracked file with tight permissions, not in the unit:

```sh
# ~/.config/assemblyai-whisper-bridge.env (untracked, chmod 600)
ASSEMBLYAI_API_KEY=<key>
```

```ini
# ~/.config/systemd/user/assemblyai-whisper-bridge.service
[Unit]
Description=AssemblyAI Whisper bridge
After=network-online.target
Wants=network-online.target

[Service]
WorkingDirectory=/path/to/assemblyai-whisper-bridge
ExecStart=/path/to/assemblyai-whisper-bridge/assemblyai-whisper-bridge
EnvironmentFile=%h/.config/assemblyai-whisper-bridge.env
Restart=on-failure

[Install]
WantedBy=default.target
```

```sh
systemctl --user daemon-reload
systemctl --user enable --now assemblyai-whisper-bridge.service
```

---

## 1. Why this exists

Voxtype is local-first, and that is its best feature. But it has an escape
hatch: `backend = "remote"` offloads transcription to a server over HTTP. That
backend is *not* an AssemblyAI client. It is a client of the **OpenAI Whisper
API dialect** — hardcoded, with no extension points:

```rust
// voxtype src/transcribe/remote.rs
let path = if self.translate {
    "/v1/audio/translations"
} else {
    "/v1/audio/transcriptions"
};
let url = format!("{}{}", self.endpoint.trim_end_matches('/'), path);
```

The path is appended in code. `remote_endpoint` is a **base URL only**, so you
cannot smuggle in a different route. There is no option for custom headers, a
custom form-field name, or a custom response shape.

AssemblyAI speaks a different dialect. So the two cannot connect directly — and
they cannot be made to connect by configuration alone. They need a translator.

That translator is this project.

### The motivation is not hypothetical

The author runs an Intel i7-8565U laptop with 7.5 GiB of RAM and roughly 2 GiB
available. Local Whisper is not viable on that hardware, and the GUI dictation
alternative (Voquill, which *does* support AssemblyAI natively) sits at **~439 MB
RSS while idle** — about 22% of available memory. A Rust daemon with
`backend = "remote"` loads no model at all and costs tens of megabytes.

So the goal is: keep the light client, keep the cloud transcription, and pay for
the difference with a small amount of code instead of a large amount of RAM.

---

## 2. The two contracts

Everything hinges on the exact shape of both sides. Both were read from source,
not from marketing pages.

### Side A — what the client sends (OpenAI Whisper dialect)

Voxtype issues a single **synchronous** `POST`, once per dictation, after the
hotkey is released:

```
POST {remote_endpoint}/v1/audio/transcriptions
Content-Type: multipart/form-data; boundary=...
Authorization: Bearer {remote_api_key}        # only if a key is configured

file             = audio.wav                  # always, 16 kHz mono WAV
model            = {remote_model}             # always
language         = {primary language}         # only when not "auto"
prompt           = {initial_prompt}           # only when configured
response_format  = "json"                     # always
```

It then reads **one field** from the JSON response:

```
{ "text": "the transcript" }                  # a top-level string, nothing else
```

Failure handling: any non-2xx, transport error, malformed JSON, or missing
`text` field raises a transcription error. There is **no automatic fallback to
local transcription** — the daemon surfaces an error status and types nothing.

If `translate = true`, the path becomes `/v1/audio/translations` instead. Out of
scope for v1.

### Side B — what AssemblyAI expects (Sync STT)

AssemblyAI's pre-recorded `v2` API is asynchronous and multi-step: submit a
JSON body containing a **publicly reachable** `audio_url`, then poll
`GET /v2/transcript/{id}` until `status == "completed"`. That is a poor fit for
push-to-talk and it is not what we target.

The right fit is AssemblyAI's **Sync STT** API, which transcribes short audio in
a single request/response with no polling:

```
POST https://sync.assemblyai.com/transcribe
Authorization: {api_key}                      # "Bearer " prefix optional
X-AAI-Model: universal-3-5-pro                # REQUIRED header

audio  = raw audio bytes                      # multipart field, NOT "file"
config = { ... }                              # optional JSON part
```

Response:

```
{ "text": "...", "words": [...], "confidence": 0.87, ... }
```

Constraints: audio between **80 ms and 120 s**, up to **40 MB**, and a **30 s
per-request deadline**. The published example shows `request_time_ms: 243.7`
for a ~101 s clip — comfortably inside a push-to-talk round trip.

### Why they don't connect

| # | Voxtype sends | AssemblyAI expects |
|---|---|---|
| 1 | path `/v1/audio/transcriptions` (hardcoded) | path `/transcribe` |
| 2 | multipart field `file` | multipart field `audio` |
| 3 | no custom headers possible | required header `X-AAI-Model` |

Auth is the one thing that already lines up: AssemblyAI accepts an optional
`Bearer` prefix, which is exactly what Voxtype sends.

All three mismatches are fixable **without touching Voxtype**.

---

## 3. What we build

A small HTTP service that:

1. Listens on `127.0.0.1:8787` (configurable).
2. Serves `POST /v1/audio/transcriptions` in the OpenAI Whisper shape.
3. Reads the multipart body, ignoring everything it does not need.
4. Forwards the audio bytes to AssemblyAI Sync STT as the `audio` part, with
   `X-AAI-Model` and the API key.
5. Returns `{"text": "..."}` — and nothing the client would choke on.

That is the whole product.

### Request mapping

| Client field | Action |
|---|---|
| `file` | Forwarded as AssemblyAI's `audio` part, `Content-Type: audio/wav` |
| `model` | **Ignored.** The model is chosen by the bridge's `X-AAI-Model` |
| `language` | **Ignored by default** — see the gotcha below |
| `prompt` | Ignored in v1 (AssemblyAI uses different context fields) |
| `response_format` | Ignored; the bridge always answers with a JSON `text` |

**Gotcha worth calling out:** Voxtype's Omarchy default config sets
`language = "en"`, and Voxtype *will* send it. For non-English dictation the
bridge must ignore it and let AssemblyAI auto-detect. Ignoring by default is
therefore the correct behaviour, not laziness.

### Client configuration

```toml
# ~/.config/voxtype/config.toml
[whisper]
backend = "remote"
remote_endpoint = "http://127.0.0.1:8787"
remote_model = "universal-3-5-pro"   # cosmetic: sent by Voxtype, ignored by us
remote_timeout_secs = 30
```

`remote_model` is inert. It is set only so the config reads sensibly to a human.
The real model selector is the bridge's `X-AAI-Model` header.

`remote_api_key` should be left unset: the bridge holds the AssemblyAI
credential, so Voxtype never needs it.

---

## 4. Deployment shape

The bridge must be running whenever dictation is used. If it is down,
transcription fails with an error and no text is typed — there is no local
fallback to catch it.

So: a **systemd user service**, `WantedBy=default.target`, `Restart=on-failure`,
started at login. It is a tiny idle process; the whole point is that it stays
out of the way.

The AssemblyAI key lives in the service's environment
(`ASSEMBLYAI_API_KEY`), never in Voxtype's config and never in the repository.

---

## 5. Non-goals for v1

- **Streaming.** AssemblyAI offers a WebSocket v3 streaming API, and streaming
  would beat batch latency. It is also a much bigger build (session lifecycle,
  chunking, partials, finalization). Batch first; streaming is a v2 candidate.
- **`/v1/audio/translations`.** Only needed when `translate = true`.
- **AssemblyAI's async `v2` pre-recorded API.** Wrong shape for push-to-talk.
- **Other upstream providers.** The bridge is deliberately a *Whisper-dialect to
  AssemblyAI* adapter, not a general gateway.
- **A GUI.** None.

---

## 6. Alternatives considered

**Configure Voquill instead (zero code).** Voquill already ships AssemblyAI as a
first-class provider, including streaming:

```tsx
assemblyai: { displayName: "AssemblyAI", testFn: assemblyaiTestIntegration },
// wss://streaming.assemblyai.com/v3/ws?sample_rate=...&token=...
```

This is the correct answer if RAM is not the binding constraint. It is rejected
here because Voquill's webview-based desktop app costs ~439 MB idle, which is
the exact problem this project exists to avoid. It remains the right choice for
anyone who wants AssemblyAI dictation today with no code.

**Patch Voxtype upstream.** Adding an AssemblyAI backend modelled on
`src/transcribe/soniox.rs` (~1600 lines) would be cleaner than a shim and would
benefit every Voxtype user. It is a larger, slower path and it depends on
upstream acceptance. Worth revisiting if the shim proves useful — and the shim
is a good way to find out.

**Use Soniox.** Voxtype already supports Soniox natively in every shipped
binary. Rejected for this use case only because it requires a paid Soniox
account, whereas the author holds free AssemblyAI credits.

---

## 7. Open questions

- **Language:** ignore always, or map Voxtype's `language` into AssemblyAI's
  `config.language_detection` / language hints? Current lean: ignore, detect.
- **Timeouts:** Voxtype's `remote_timeout_secs` is 30 by default and AssemblyAI's
  own per-request deadline is 30 s. Are these aligned enough, or should the
  bridge fail fast with a clearer error?
- **Audio format:** Voxtype sends 16 kHz mono WAV. Confirm AssemblyAI's Sync API
  accepts it as-is without a `config.sample_rate` part.
- **Error surface:** what should the bridge return when AssemblyAI rejects the
  request (quota, auth, too long)? Voxtype will show a generic error, so the
  message matters more than the code.
- **Implementation language:** Python (fastest to write, needs a runtime) vs Go
  or Rust (single static binary, better fit for a long-lived tiny daemon).
- **Name:** `assemblyai-whisper-bridge` is descriptive and vendor-neutral enough
  to publish, but it is cheap to change now and expensive later.

---

## 8. References

- Voxtype source: `src/transcribe/remote.rs`, `src/transcribe/soniox.rs`,
  `src/daemon.rs`, `src/transcribe/worker.rs`
- Voxtype remote docs: `docs/USER_MANUAL.md` → *Remote Whisper Servers*
- AssemblyAI Sync STT: `https://sync.assemblyai.com/transcribe`
- AssemblyAI pre-recorded v2: `POST /v2/transcript` + polling
- Omarchy integration: `omarchy voxtype install|config|model|remove|status`
