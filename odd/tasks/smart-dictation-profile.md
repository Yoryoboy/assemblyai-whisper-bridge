# Smart Dictation Profile

## Objective
Add a `smart` Voxtype profile that routes dictated text through the Pi CLI so a spoken
instruction can transform the dictation (for example, elaborate an email), while
dictation without an instruction receives only punctuation and capitalization cleanup.

## Problem and why
Raw dictation is verbatim. The user wants a second, explicit path where the same audio can
carry an instruction ("clave instrucciones ... clave contexto ...") and produce a finished
piece of writing, plus a lighter path that just tidies punctuation. The raw path must stay
untouched so it never depends on a model.

## Scope
- One wrapper script that Voxtype executes as the profile's post-process command.
- One installer that applies the whole configuration to a machine and can be re-run.
- The repository is the source of truth for the prompt, the rules and the installer; the
  installer deploys the wrapper and edits Voxtype's live configuration.
- One Hyprland binding for the new profile.
- Documentation of the new path in the README.

## Constraints
- The raw path (`SUPER + H` -> `voxtype record toggle`) must not change behavior.
- Never write the AssemblyAI API key, the Pi credentials or the user's identity into the
  repository.
- The installer must not clobber unrelated Voxtype settings or comments.
- Technical artifacts are written in English.
- Do not commit without explicit user authorization (standing project constraint).

## Delivery
- Strategy: ask-on-risk.
- Forecast: well under 400 authored changed lines (two small shell scripts, one template,
  README section).
- Native review candidate: a work-unit commit, only after explicit commit authorization.

## Locked design decisions
| Decision | Value |
|---|---|
| Raw shortcut | `SUPER + H` -> `voxtype record toggle` (unchanged) |
| Profile shortcut | `SUPER + SHIFT + H` -> `voxtype record toggle --profile smart` |
| Profile name | `smart` |
| Model | `pi -p --provider opencode-go --model deepseek-v4-flash` |
| Pi flags | `--no-tools --no-session --thinking off --no-context-files --mode text` |
| Output mode | `paste` (copies, then sends the paste keys; never types character by character) |
| Post-process timeout | `120000` ms |
| Labels | spoken `clave instrucciones` / `clave contexto` normalised to `INSTRUCCIONES:` / `CONTEXTO:` |
| No labels | punctuation and capitalization only |
| Fillers | native conservative list only; the model never touches fillers |
| `VOXTYPE_CONTEXT` | deliberately ignored |
| Identity | local git-ignored file; never committed |

Verified facts this design depends on:
- The text pipeline order is: transcription -> filler filter -> `[text] replacements` ->
  `[post_process]`. So `replacements` normalises the spoken labels before Pi sees them
  (`docs/CONFIGURATION.md`, `docs/USER_MANUAL.md`).
- `post_process_command` is a shell command, so `$HOME` expands in it.
- On any post-process failure or timeout Voxtype falls back to the original transcription,
  so a missing Pi degrades to raw dictation instead of breaking.
- `text.filler_words` is an array that **replaces** the whole list, and neither it nor
  `output.type_delay_ms` nor `[profiles.*]` are settable through `voxtype config set`
  (the CLI only accepts keys from the `config schema` allowlist).
- `filter_filler_words` defaults to `true`; its default list is
  `["uh","um","er","ah","eh","hmm","hm","mm","mhm"]`.

## Tasks
- [x] **SP-1 — Wrapper script** (`voxtype/voxtype-smart.sh`)
  - Read the dictation from stdin, build the system prompt, invoke Pi, print the result on
    stdout and nothing else.
  - Check: `bash -n voxtype/voxtype-smart.sh`; a run with a stub `pi` on `PATH` returns only
    the stub's stdout; a run with the identity file absent omits the signature section.
  - Acceptance: stdout carries only the transformed text; the startup banner never leaks to
    stdout; a failing Pi exits non-zero so Voxtype falls back to raw text.
- [x] **SP-2 — Installer** (`voxtype/install-smart-profile.sh`)
  - Preflight, backup, hybrid apply, self-verification, idempotent re-run.
  - Check: `bash -n`; run against a sandbox copy of `config.toml` and confirm the sandbox is
    the only file changed; a second run produces no diff.
  - Acceptance: unrelated keys and comments survive; `type_delay_ms`, `filler_words` and
    `replacements` are edited inside their existing tables; `[profiles.smart]` is appended
    once; the installer fails loudly and changes nothing when a prerequisite is missing.
- [x] **SP-3 — Identity template and README** (`voxtype/identity.env.example`, README)
  - Document the new path, the labels, the prerequisites and the per-machine steps.
  - Check: every documented command and path matches the scripts.
- [x] **SP-4 — Apply on this machine and verify live** (parent, not delegated)
  - Run the installer, restart Voxtype, confirm `voxtype config` resolves the new values,
    and confirm the raw path still works.

## SP-1 specification
Files: `voxtype/voxtype-smart.sh` (executable, bash, `set -euo pipefail`).

Behavior:
1. `unset VOXTYPE_CONTEXT` — the continuity variable is deliberately ignored.
2. Read the dictation: `input=$(cat)`.
3. If `$input` is empty or whitespace only, exit 0 printing nothing (no model call).
4. Build `PROMPT` with the protocol text below.
5. If `${HOME}/.config/voxtype/identity.env` exists, source it and append the signature
   section containing the identity; otherwise omit the signature section entirely.
6. If `$input` starts with `@`, prefix a single space so Pi never parses it as a file
   attachment.
7. `exec pi -p --provider opencode-go --model deepseek-v4-flash --mode text --no-tools
   --no-session --thinking off --no-context-files --system-prompt "$PROMPT" -- "$input"`.
8. Never add `--no-extensions`: the `opencode-go` provider is extension-provided.

Protocol prompt text (transcribe verbatim):
```
Sos un corrector de dictado. Recibís texto dictado por voz. Devolvés SOLO el texto
resultante: sin explicaciones, sin preámbulos, sin comillas que lo envuelvan, sin
comentarios sobre lo que hiciste.

Hay dos casos.

CASO A - el texto contiene la etiqueta "INSTRUCCIONES:".
  - Lo que va entre "INSTRUCCIONES:" y "CONTEXTO:" (o hasta el final, si no hay
    "CONTEXTO:") es la instrucción: QUÉ hay que hacer. La etiqueta "CONTEXTO:", si
    existe, aporta para quién, por qué, con qué tono, en qué idioma y por qué canal.
  - Todo lo que va después de la instrucción y del contexto es el CUERPO a transformar.
  - Aplicá la instrucción al cuerpo. Si la instrucción pide reescribir, redactar,
    elaborar o convertir -por ejemplo un mail o un mensaje- reescribí con libertad de
    forma: estructura, registro y las convenciones propias del formato, incluido markdown
    si el formato lo pide.
  - El idioma de salida es el que pidan la instrucción o el contexto. Si no lo dicen,
    es español.

CASO B - el texto NO contiene "INSTRUCCIONES:".
  - No transformes ni reestructures nada. Corregí únicamente puntuación y mayúsculas.
    Mismo contenido, mismas palabras, mismo orden, mismo registro. No agregues ni saques
    nada salvo puntuación.

REGLAS QUE APLICAN SIEMPRE
- Nunca inventes datos: nombres de personas, fechas, números, direcciones, compromisos ni
  hechos que no estén en el texto.
- Nunca uses guion largo ni guion medio. Usá coma, punto o dos puntos.
- Prohibido el estilo de IA: aperturas huecas como "espero que te encuentres bien",
  cierres genéricos como "quedo a disposición" o "no dudes en contactarme", muletillas
  como "en resumen", "cabe destacar" o "es importante señalar", arranques como "claro" o
  "por supuesto", emojis, viñetas decorativas y comentarios sobre tu propio trabajo.
- No repitas el texto original, no lo resumas y no lo expliques.
- Ante la duda, cambiá menos.
```

## SP-2 specification
Files: `voxtype/install-smart-profile.sh` (executable, bash, `set -euo pipefail`),
`voxtype/identity.env.example`.

Steps:
1. **Preflight** (all checks, collect every failure, apply nothing if any fails):
   `voxtype` on PATH; `pi` on PATH; `pi auth check --provider opencode-go --no-refresh
   --json` reports `"status":"ready"`; the bridge answers on `127.0.0.1:8787` (any HTTP
   status counts, since the only route is POST-only); `${REPO}/.env` exists and defines
   `ASSEMBLYAI_API_KEY`.
2. **Backup** `~/.config/voxtype/config.toml` to a timestamped sibling.
3. **Scalars via the CLI** (it validates and preserves comments):
   `voxtype config set audio.feedback.enabled true`;
   `voxtype config set audio.feedback.theme subtle`;
   `voxtype config set audio.max_duration_secs 110`.
4. **In-place edits via python3** (these keys are not in the CLI allowlist, and TOML
   forbids redeclaring an existing table, so they must be edited inside their tables):
   - `[output]`: set or insert `type_delay_ms = 0`.
   - `[text]`: set or insert `filler_words` with the full list
     `["uh","um","er","ah","eh","hmm","hm","mm","mhm","em","mmm","o sea"]`;
     append the two label pairs to the existing `replacements` inline table:
     `"clave instrucciones" = "INSTRUCCIONES:"` and `"clave contexto" = "CONTEXTO:"`.
5. **Managed block appended once**, delimited by
   `# >>> smart-dictation-profile (managed) >>>` and
   `# <<< smart-dictation-profile (managed) <<<`, containing `[profiles.smart]` with
   `post_process_command = "$HOME/.config/voxtype/voxtype-smart.sh"`,
   `output_mode = "clipboard"` and `post_process_timeout_ms = 120000`. A re-run replaces
   the block instead of appending a second one.
6. **Deploy the wrapper**: copy `voxtype/voxtype-smart.sh` to
   `~/.config/voxtype/voxtype-smart.sh`, `chmod 755`.
7. **Self-verify** by reading `voxtype config` back and asserting the resolved values, then
   print the exact Hyprland binding to add and the restart command.

## Progress
- Branch `feat/smart-dictation-profile` created from `main`; repository was on the default
  branch.
- Design closed with the user over five structured rounds; all decisions recorded above.
- **Delegation unavailable.** Two bounded dispatches to a writer sub-agent failed with
  `JSON schema exceeds the maximum nesting depth of 10 levels`, first with a 400-line prompt
  and then with a 40-line one. The failure is an environment/runtime defect, not a prompt
  problem, so the implementation was executed inline instead of blocking on it. This is a
  recorded deviation from the delegated-writer route.
- SP-1 to SP-3 implemented and verified. SP-4 applied on this machine but NOT verified
  end-to-end: the installer ran live (preflight ok, backup, nine assertions passed), the
  wrapper was deployed byte-identical to the repository copy, the Hyprland binding was added
  and the file passed `luac -p`, Hyprland reloaded cleanly and the Voxtype daemon restarted
  with audio feedback active.
- SP-4 open item: whether the profile actually reaches the wrapper at transcription time is
  still unproven. A differential test (`--profile no-existe-xyz` versus `--profile smart`,
  each followed by cancel) produced no profile log line in either case, because Voxtype only
  consults the profile during post-processing. That test is therefore inconclusive and is
  recorded as such rather than reported as a pass. A single real dictation through
  `SUPER + SHIFT + H` is the actual proof.
- SP-4 live result (user-reported): the transformation worked well and the finished text
  landed in the clipboard intact, so the wrapper, the Pi invocation and the label protocol
  are all confirmed in real use. The one defect found was output delivery: `output_mode =
  "clipboard"` copies but never pastes, so nothing reached the focused input.
- Fix: the profile now uses `output_mode = "paste"`, which copies and then sends the paste
  keys. `restore_clipboard` stays `false`, so the clipboard keeps the text afterwards and
  doubles as a fallback if the paste does not land. `wait_for_modifier_release` is already
  `true`, which matters here because the shortcut holds SUPER and SHIFT while the paste
  fires. A tenth self-verification check now asserts the paste mode in the written file.
- SP-4 closed: the user confirmed in live use that the text now pastes into the focused
  field. Both halves of the feature are verified in real use — the transformation (labels,
  Pi, the protocol) and the delivery (`paste` mode).

- Environment note: `go` is currently unconfigured in mise (`No version is set for shim:
  go`), so the existing Go suite cannot run from this shell. Pre-existing condition,
  unrelated to this change, and not repaired here.

## Verification evidence
All checks below were run and their output observed; none is inferred.

- Syntax: `bash -n voxtype/voxtype-smart.sh` and `bash -n voxtype/install-smart-profile.sh`
  both clean.
- Wrapper, clean run: with a stub `pi` on `PATH` that writes a known banner to stderr, the
  wrapper printed only the stub's stdout (`STUB_OUT`), kept the banner on stderr, and exited
  0.
- Wrapper, failure propagation: with a stub that exits 3, the wrapper exited 3. This is what
  makes Voxtype fall back to the raw transcription.
- Wrapper, empty input: whitespace-only stdin exited 0 and never invoked `pi` (the stub's
  dump file was not created).
- Wrapper, identity branch: with no `identity.env`, the system prompt contained no `FIRMA`
  section (count 0); with a non-empty `DICTATION_IDENTITY`, it contained exactly one, carrying
  that value.
- Wrapper, `@` guard: stdin `@mencion` was passed to `pi` as ` @mencion`, so Pi cannot read
  it as a file attachment.
- Installer, preflight: with `pi` absent from `PATH`, it exited 1, reported the single
  missing prerequisite, created no backup and left the sandbox configuration byte-identical.
- Installer, sandbox apply: ten self-verification checks passed (resolved `type_delay_ms = 0`,
  `feedback.enabled = true`, `theme = "subtle"`, Spanish filler entries present, both label
  replacements present, `[profiles.smart]` present, `output_mode = "paste"`, exactly one
  managed block, wrapper deployed and executable).
- Installer, idempotency: two consecutive runs produced the same configuration hash.
- Installer, backup integrity: in a clean directory the first run's backup matched the
  pristine original byte for byte, and the second run wrote a distinct file rather than
  clobbering it.
- Comment preservation: the diff against the original showed only the intended additions;
  existing comments, `audio.device` and all unrelated keys survived untouched.
- Isolated CLI writes: `voxtype -c <file> config set ...` was proven to edit only the target
  file, with the live configuration hash unchanged, which is what makes the sandbox test
  meaningful.
- Discovered, not a defect: `voxtype config set` itself creates a `config.toml.bak` next to
  the file it edits. Confirmed by running a single `config set` in an empty directory. The
  installer's own timestamped backup is created before any `config set` runs and holds the
  pristine state.

## Deviations from the specification
- The profile's `post_process_command` embeds the **absolute** deployed wrapper path instead
  of a literal `$HOME/...`. The documentation calls the hook a shell command, but shell
  expansion was never directly proven, and an absolute path is correct either way. The
  installer regenerates it per machine, so portability is unaffected.
- `text.replacements` is edited in place rather than through `voxtype config set`, because
  the replacement keys contain spaces and the CLI's dotted-path handling for such keys was
  not verified. The in-place editor restates the full existing pair list, so no entry is lost.

## Next step
Only the commit remains. The branch `feat/smart-dictation-profile` holds the wrapper, the
installer, the identity template, README section 9, the archify diagrams in `docs/` and this
task doc. Commit authorization is still pending from the user.
