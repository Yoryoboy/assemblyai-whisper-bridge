# Local MVP

## Objective
Build the smallest useful local OpenAI-compatible transcription bridge backed by AssemblyAI Sync STT.

## Problem and why
Voxtype speaks the OpenAI Whisper HTTP dialect, while AssemblyAI Sync STT expects a different path, multipart field, and model header. A lightweight local adapter avoids running a large desktop dictation client.

## Scope
- Go HTTP service bound to localhost by default.
- `POST /v1/audio/transcriptions` accepting Voxtype's multipart request.
- Forward the uploaded WAV to AssemblyAI Sync STT.
- Return only the OpenAI-compatible `text` response.
- Minimal configuration through environment variables.
- Focused tests and simple local run instructions.
- Local manual test with the competing dictation app disabled so the shortcut is available.

## Constraints
- Personal local use; prioritize simplicity and speed over production hardening.
- No streaming, translations, GUI, provider abstraction, database, or elaborate security framework.
- Keep the AssemblyAI key out of tracked files.
- Technical artifacts are written in English.
- Do not commit without explicit user authorization.

## Delivery
- Strategy: ask-on-risk.
- Forecast: under 400 authored changed lines for the MVP.
- Native review candidate: a future work-unit commit, only after explicit commit authorization.

## Tasks
- [x] **MVP-1 — Implement and test the bridge** (delegated; multi-file write trigger)
  - Add a minimal Go module, HTTP handler, AssemblyAI client, and focused tests.
  - Check: `go test ./...`
  - Acceptance: valid multipart audio is forwarded as `audio` with auth and model headers, and successful upstream JSON becomes `{ "text": "..." }`.
- [x] **MVP-2 — Document local operation** (delegated; documentation verification required)
  - Add concise build/run/configuration instructions and a minimal systemd user unit example.
  - Check: commands and environment names match the implementation.
- [x] **MVP-3 — Run the local Voxtype integration check** (live user verification)
  - Disable the competing shortcut owner temporarily, configure Voxtype, run one dictation, and restore or document the previous state.
  - Check: spoken audio returns text through the bridge.

## Progress
- Repository inspection complete. Git was already initialized; it currently has no commits.
- Implementation language selected: Go.
- MVP-1 complete: `main.go` + `main_test.go` implement the bridge (Whisper `file` field forwarded as upstream `audio` with auth/`X-AAI-Model` headers; successful upstream JSON becomes `{ "text": "..." }`).
- MVP-2 complete: README local-operation instructions were independently checked against `main.go`.
- Current task: MVP-3. Voxtype 1.0.1 is installed and configured with `[whisper] mode = "remote"` to use `http://127.0.0.1:8787` exclusively.
- Local model residency was removed: Voxtype idle memory dropped from about 202 MB to about 35 MB after restart.
- Voquill is stopped temporarily, `SUPER + H` is rebound to `voxtype record toggle`, and the bridge is running as a transient user service on `127.0.0.1:8787` using the project `.env`.
- Project-local `.env` support is complete and verified with 12 passing tests.
- MVP-3 complete: the user dictated a message through Voxtype and confirmed successful text insertion.

## Verification evidence
- MVP-1: `go test ./...` passes (5 tests: success mapping, missing file 400, wrong method 405, upstream non-2xx 502, upstream bad JSON 502). `go vet ./...` clean, `gofmt -l .` clean. Re-verified this session: `go test ./...` passes (5 passed in 1 package). MVP-1 checkbox marked complete from this evidence.
- MVP-2: README documents local build/run, exact environment names/defaults from `main.go`, and a minimal systemd user unit. Independent verification confirmed all four environment variables match, the service example is internally consistent, no secret is present, and `go test ./...` still passes.
- `.env` support (unreconciled): focused loader/precedence tests added; run the Verification commands below for evidence. Completion left to parent reconciliation.
- `.env` verification (unreconciled): `go test ./...` 10 passed (5 bridge + 5 dotenv), `go vet ./...` clean, `gofmt -l .` clean after formatting; `git check-ignore` confirms `.env` is ignored. Debugging note: an early helper version used `defer` inside the helper (runs at helper return, not test end) and leaked env between tests; fixed with `t.Cleanup` snapshot/restore.
- `.env` corrections verification: `go test ./...` 12 passed in 1 package; `go vet ./...` clean; `gofmt -l .` clean; `.env` and the build binary are ignored. No secret was read or printed.
- MVP-3: live end-to-end verification passed; `SUPER + H` recorded audio, the bridge sent it to AssemblyAI, and Voxtype inserted the returned text.

## Next step
Optionally replace the transient bridge unit with a persistent systemd user service so it starts automatically after login.
