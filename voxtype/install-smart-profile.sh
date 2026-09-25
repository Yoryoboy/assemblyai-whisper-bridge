#!/usr/bin/env bash
#
# Applies the `smart` dictation profile to a Voxtype installation.
#
# The repository is the source of truth. This installer:
#   1. verifies every prerequisite and refuses to apply anything if one is missing
#   2. backs up the configuration
#   3. applies scalar keys through `voxtype config set` (validated, comment preserving)
#   4. edits keys outside the CLI allowlist in place, inside their own tables
#   5. appends one delimited managed block for [profiles.smart]
#   6. deploys the wrapper script next to the configuration
#   7. reads the result back and asserts it
#
# It is idempotent: a second run changes nothing.
#
# Environment overrides (used for sandbox testing):
#   SMART_CONFIG          configuration file to edit (default ~/.config/voxtype/config.toml)
#   SMART_WRAPPER_PATH    deployed wrapper path (default ~/.config/voxtype/voxtype-smart.sh)
#   SMART_SKIP_PREFLIGHT  set to 1 to skip prerequisite checks
#
set -euo pipefail

REPO_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
WRAPPER_SRC="${REPO_DIR}/voxtype/voxtype-smart.sh"
CONFIG="${SMART_CONFIG:-${HOME}/.config/voxtype/config.toml}"
WRAPPER_DST="${SMART_WRAPPER_PATH:-${HOME}/.config/voxtype/voxtype-smart.sh}"
SKIP_PREFLIGHT="${SMART_SKIP_PREFLIGHT:-0}"

PROFILE_NAME="smart"
PI_PROVIDER="opencode-go"
BLOCK_BEGIN="# >>> smart-dictation-profile (managed) >>>"
BLOCK_END="# <<< smart-dictation-profile (managed) <<<"

fail() { printf 'error: %s\n' "$*" >&2; }
note() { printf '%s\n' "$*"; }

# --------------------------------------------------------------- preflight
if [[ "$SKIP_PREFLIGHT" != "1" ]]; then
  failures=()

  command -v voxtype >/dev/null 2>&1 || failures+=("voxtype is not on PATH")
  command -v pi >/dev/null 2>&1 || failures+=("pi is not on PATH")

  if command -v pi >/dev/null 2>&1; then
    if ! timeout 60 pi auth check --provider "$PI_PROVIDER" --no-refresh --json 2>/dev/null \
        | grep -q '"status":"ready"'; then
      failures+=("pi provider '${PI_PROVIDER}' is not ready")
    fi
  fi

  # A listening socket is enough: the bridge only serves a POST route, so an
  # HTTP probe would be an error response even on a healthy bridge.
  if ! timeout 5 bash -c 'exec 3<>/dev/tcp/127.0.0.1/8787' 2>/dev/null; then
    failures+=("nothing is listening on 127.0.0.1:8787")
  fi

  if ! grep -qE '^[[:space:]]*ASSEMBLYAI_API_KEY[[:space:]]*=' "${REPO_DIR}/.env" 2>/dev/null; then
    failures+=("${REPO_DIR}/.env does not define ASSEMBLYAI_API_KEY")
  fi

  if ((${#failures[@]})); then
    fail "prerequisites are not satisfied; nothing was changed:"
    for f in "${failures[@]}"; do printf '  - %s\n' "$f" >&2; done
    exit 1
  fi
  note "preflight: ok"
else
  note "preflight: skipped (SMART_SKIP_PREFLIGHT=1)"
fi

[[ -f "$WRAPPER_SRC" ]] || { fail "wrapper not found at ${WRAPPER_SRC}"; exit 1; }
[[ -f "$CONFIG" ]] || { fail "configuration not found at ${CONFIG}"; exit 1; }

# ------------------------------------------------------------------ backup
# Nanoseconds keep two runs in the same second from clobbering each other's
# backup, which would otherwise overwrite the pristine copy.
backup="${CONFIG}.bak-$(date +%Y%m%d-%H%M%S-%N)"
cp -p "$CONFIG" "$backup"
note "backup: ${backup}"

# --------------------------------------- scalars the CLI validates for us
voxtype -c "$CONFIG" config set audio.feedback.enabled true >/dev/null
voxtype -c "$CONFIG" config set audio.feedback.theme subtle >/dev/null
voxtype -c "$CONFIG" config set audio.max_duration_secs 110 >/dev/null
note "scalars: audio.feedback.enabled=true theme=subtle max_duration_secs=110"

# ------------------- in-place edits for keys outside the CLI allowlist
python3 - "$CONFIG" "$WRAPPER_DST" "$BLOCK_BEGIN" "$BLOCK_END" "$PROFILE_NAME" <<'PY'
import re
import sys

path, wrapper, block_begin, block_end, profile = sys.argv[1:6]

FILLER_LINE = (
    'filler_words = ["uh", "um", "er", "ah", "eh", "hmm", "hm", "mm", '
    '"mhm", "em", "mmm", "o sea"]'
)
LABEL_PAIRS = [("clave instrucciones", "INSTRUCCIONES:"), ("clave contexto", "CONTEXTO:")]

with open(path, encoding="utf-8") as handle:
    original = handle.read()
lines = original.split("\n")


def section_bounds(name):
    """Return (start, end) for the top-level [name] table, or (None, None)."""
    start = None
    for i, line in enumerate(lines):
        match = re.match(r"^\s*\[([^\]]+)\]\s*$", line)
        if not match:
            continue
        if match.group(1) == name:
            start = i
        elif start is not None:
            return start, i
    return (start, len(lines)) if start is not None else (None, None)


def set_key(section, key, new_line):
    start, end = section_bounds(section)
    if start is None:
        sys.exit(f"error: section [{section}] not found in {path}")
    for i in range(start + 1, end):
        if re.match(rf"^\s*{re.escape(key)}\s*=", lines[i]):
            lines[i] = new_line
            return
    lines.insert(start + 1, new_line)


def add_replacements(section, pairs):
    start, end = section_bounds(section)
    if start is None:
        sys.exit(f"error: section [{section}] not found in {path}")
    for i in range(start + 1, end):
        if not re.match(r"^\s*replacements\s*=", lines[i]):
            continue
        existing = re.findall(r'"([^"]*)"\s*=\s*"([^"]*)"', lines[i])
        known = {key for key, _ in existing}
        merged = list(existing) + [pair for pair in pairs if pair[0] not in known]
        if len(merged) == len(existing):
            return
        body = ", ".join(f'"{key}" = "{value}"' for key, value in merged)
        lines[i] = f"replacements = {{ {body} }}"
        return
    body = ", ".join(f'"{key}" = "{value}"' for key, value in pairs)
    lines.insert(start + 1, f"replacements = {{ {body} }}")


def strip_managed_block():
    kept, skipping = [], False
    for line in lines:
        if line.strip() == block_begin:
            skipping = True
            continue
        if line.strip() == block_end:
            skipping = False
            continue
        if not skipping:
            kept.append(line)
    return kept


lines = strip_managed_block()
set_key("output", "type_delay_ms", "type_delay_ms = 0")
set_key("text", "filler_words", FILLER_LINE)
add_replacements("text", LABEL_PAIRS)

while lines and lines[-1].strip() == "":
    lines.pop()

lines.extend(
    [
        "",
        block_begin,
        f"[profiles.{profile}]",
        f'post_process_command = "{wrapper}"',
        'output_mode = "paste"',
        "post_process_timeout_ms = 120000",
        block_end,
        "",
    ]
)

updated = "\n".join(lines)
if updated == original:
    print("config: already up to date")
else:
    with open(path, "w", encoding="utf-8") as handle:
        handle.write(updated)
    print("config: updated")
PY

# ---------------------------------------------------------- deploy wrapper
install -m 755 "$WRAPPER_SRC" "$WRAPPER_DST"
note "wrapper: ${WRAPPER_DST}"

# -------------------------------------------------------------- verify
resolved="$(voxtype -c "$CONFIG" config 2>&1)" || {
  fail "the resulting configuration is not valid:"
  printf '%s\n' "$resolved" >&2
  exit 1
}

problems=()
record() {
  if [[ "$2" == "1" ]]; then
    note "  ok   $1"
  else
    problems+=("$1")
    note "  FAIL $1"
  fi
}

contains() { grep -qF -- "$2" <<<"$1" && echo 1 || echo 0; }

record "resolved output.type_delay_ms is 0" \
  "$(contains "$resolved" 'type_delay_ms = 0')"
record "resolved audio.feedback.enabled is true" \
  "$(contains "$resolved" 'enabled = true')"
record "resolved audio.feedback.theme is subtle" \
  "$(contains "$resolved" 'theme = "subtle"')"
record "filler list includes the Spanish additions" \
  "$([[ "$(grep -c 'o sea' <<<"$(<"$CONFIG")")" -ge 1 ]] && echo 1 || echo 0)"
record "label replacement for instructions is present" \
  "$(contains "$(<"$CONFIG")" '"clave instrucciones" = "INSTRUCCIONES:"')"
record "label replacement for context is present" \
  "$(contains "$(<"$CONFIG")" '"clave contexto" = "CONTEXTO:"')"
record "profile table is present" \
  "$(contains "$(<"$CONFIG")" "[profiles.${PROFILE_NAME}]")"
record "profile output mode is paste" \
  "$(contains "$(<"$CONFIG")" 'output_mode = "paste"')"
record "exactly one managed block" \
  "$([[ "$(grep -cF -- "$BLOCK_BEGIN" "$CONFIG")" == "1" ]] && echo 1 || echo 0)"
record "wrapper is deployed and executable" \
  "$([[ -x "$WRAPPER_DST" ]] && echo 1 || echo 0)"

if ((${#problems[@]})); then
  fail "${#problems[@]} verification check(s) failed"
  exit 1
fi

# ------------------------------------------------------------- summary
cat <<SUMMARY

Done. The smart profile is installed and verified.

Add this binding to ~/.config/hypr/bindings.lua:

  o.bind("SUPER + SHIFT + H", "Voxtype smart", "voxtype record toggle --profile ${PROFILE_NAME}")

Then restart the daemon to pick the configuration up:

  systemctl --user restart voxtype

Optional: to enable the signature rule, create ~/.config/voxtype/identity.env
with DICTATION_IDENTITY="Your Name". Without that file the profile omits it.

Rollback: cp "${backup}" "${CONFIG}" && systemctl --user restart voxtype
SUMMARY
