#!/usr/bin/env bash
#
# Voxtype post-process wrapper for the `smart` profile.
#
# Voxtype pipes the dictated text in on stdin and reads the transformed text
# from stdout. On any non-zero exit Voxtype falls back to the raw
# transcription, so failing loudly here is safe.
#
# Deployed to ~/.config/voxtype/voxtype-smart.sh by install-smart-profile.sh.
#
set -euo pipefail

# The previous dictation's text is deliberately ignored. With the label
# protocol below, stale text would be misread as content.
unset VOXTYPE_CONTEXT

PI_BIN="${PI_BIN:-pi}"
PI_PROVIDER="${PI_PROVIDER:-opencode-go}"
PI_MODEL="${PI_MODEL:-deepseek-v4-flash}"
IDENTITY_FILE="${DICTATION_IDENTITY_FILE:-${HOME}/.config/voxtype/identity.env}"

input="$(cat)"

# An empty or whitespace-only dictation never reaches the model.
if [[ -z "${input//[[:space:]]/}" ]]; then
  exit 0
fi

PROMPT="$(cat <<'PROTOCOL'
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
PROTOCOL
)"

# The signature rule exists only when an identity is configured locally.
if [[ -f "$IDENTITY_FILE" ]]; then
  # shellcheck disable=SC1090
  source "$IDENTITY_FILE"
  if [[ -n "${DICTATION_IDENTITY:-}" ]]; then
    PROMPT="${PROMPT}"$'\n\nFIRMA'
    PROMPT="${PROMPT}"$'\n'"- Sólo si la instrucción implica que el texto es un mensaje o un mail que se envía, cerralo con la firma indicada abajo."
    PROMPT="${PROMPT}"$'\n'"- Si el texto no es un mensaje que se envía, no agregues firma."
    PROMPT="${PROMPT}"$'\n'"Firma: ${DICTATION_IDENTITY}"
  fi
fi

# A leading '@' would make Pi treat the message as a file attachment.
if [[ "$input" == @* ]]; then
  input=" ${input}"
fi

# --no-extensions is intentionally absent: the opencode-go provider is
# extension-provided and disabling extensions removes it.
exec "$PI_BIN" -p \
  --provider "$PI_PROVIDER" \
  --model "$PI_MODEL" \
  --mode text \
  --no-tools \
  --no-session \
  --thinking off \
  --no-context-files \
  --system-prompt "$PROMPT" \
  -- "$input"
