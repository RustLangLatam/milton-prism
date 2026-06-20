#!/usr/bin/env bash
# Milton Prism — TaskCompleted hook (Claude Code agent teams)
#
# Se dispara cuando un teammate va a marcar una tarea como completa.
# Contrato Claude Code: exit 2 PREVIENE el completado y manda lo de stderr como feedback al teammate.
# Cualquier otro exit (0) deja completar.
#
# QUÉ ENFORZA (mecánico, confiable):
#   - go build / vet / test del backend; tsc del frontend (lo que aplique y esté presente).
#   - greps prohibidos (patrones anti-lección que DEBEN dar 0 matches).
#   - presencia de evidencia de verificación (el teammate debe registrar qué validó).
# QUÉ **NO** ENFORZA (requiere juicio humano / del lead — ver lecciones 6, 9, 11):
#   - que la validación haya sido contra la INSTANCIA persistida CORRECTA (no un homónimo).
#   - que el teammate no esté falsando una premisa ("es del entorno", "preexistente").
#   Para esos, mantené plan-approval y revisá los ítems de capacidad (CI3, PHP-no-genera).
#
# SETUP: hacelo ejecutable (chmod +x) y registralo en settings.json (ver instrucciones del handoff).
# Ajustá las rutas y los patrones a tu repo. Un patrón mal puesto bloquea de más; uno faltante deja pasar bugs.

set -uo pipefail

# ── Rutas (AJUSTAR) ───────────────────────────────────────────────────────────
BACKEND_DIR="${MILTON_BACKEND_DIR:/mnt/usr/src/desarrollo/gopath/RustLangLatam/milton-prism}"
FRONTEND_DIR="${MILTON_FRONTEND_DIR:/mnt/usr/src/desarrollo/js/RustLangLatam/milton-prism-panel}"
# Archivo donde el teammate registra evidencia (summary_id validado + resultado HTTP real):
EVIDENCE_FILE="${MILTON_EVIDENCE_FILE:-$BACKEND_DIR/.milton/last-verification.md}"
EVIDENCE_MAX_AGE_MIN="${MILTON_EVIDENCE_MAX_AGE_MIN:-30}"

payload="$(cat)"   # JSON del hook por stdin (task info); se usa solo para contexto del feedback.
fail() { echo "BLOQUEADO (TaskCompleted): $1" >&2; exit 2; }

# ── 1. Backend: gates Go (solo si el árbol existe y hay go) ───────────────────
if [[ -d "$BACKEND_DIR" ]] && command -v go >/dev/null 2>&1; then
  pushd "$BACKEND_DIR" >/dev/null || fail "no pude entrar a BACKEND_DIR=$BACKEND_DIR"
  if command -v buf >/dev/null 2>&1 && [[ -f buf.yaml || -f buf.gen.yaml ]]; then
    buf lint >/dev/null 2>&1 || fail "buf lint falló. Lección 1: si tocaste protos, regenerá y rebuildeá toda la cadena."
  fi
  go build ./... >/dev/null 2>&1 || fail "go build ./... falló. 'No compila' no es completable."
  go vet ./...   >/dev/null 2>&1 || fail "go vet ./... no está limpio."
  # test scopeado como en el ESTADO; ajustá si tu suite verde es otra:
  go test ./core/... >/dev/null 2>&1 || fail "go test ./core/... no está verde. Lección 2: pasar tests es el piso, no la prueba de que funciona."
  popd >/dev/null
fi

# ── 2. Frontend: tsc (solo si existe y hay tooling) ───────────────────────────
if [[ -d "$FRONTEND_DIR" ]] && command -v npx >/dev/null 2>&1 && [[ -f "$FRONTEND_DIR/tsconfig.json" ]]; then
  ( cd "$FRONTEND_DIR" && npx --no-install tsc --noEmit >/dev/null 2>&1 ) \
    || fail "tsc del frontend falló."
fi

# ── 3. Greps prohibidos (DEBEN dar 0 matches) ─────────────────────────────────
# Cada entrada: "descripción|||ruta|||patrón_regex_extendida". Extendé esta lista.
# El objetivo NO es exhaustividad semántica (imposible en un grep) sino atrapar las anti-lecciones conocidas.
FORBIDDEN=(
  "Vocabulario de kind viejo con guión (frente 3: debe ser underscore)|||$FRONTEND_DIR|||'shared-state'|'god-module'"
  "Oráculo de ciclos viejo en el frontend (frente 3: migrado al backend)|||$FRONTEND_DIR|||detectGraphCycles|buildCycleFindings"
  "Secreto del esqueleto de dev filtrado al payload generado (assertNoSecrets)|||$BACKEND_DIR|||signKey|mongodb://|redis://"
)
for entry in "${FORBIDDEN[@]}"; do
  desc="${entry%%|||*}"; rest="${entry#*|||}"; dir="${rest%%|||*}"; pat="${rest#*|||}"
  [[ -d "$dir" ]] || continue
  if grep -REn --include='*.go' --include='*.ts' --include='*.tsx' "$pat" "$dir" >/dev/null 2>&1; then
    hits="$(grep -REn --include='*.go' --include='*.ts' --include='*.tsx' "$pat" "$dir" 2>/dev/null | head -5)"
    fail "grep prohibido NO dio 0 — $desc. Lección 8 (cierre = grep=0, no la primera llamada). Coincidencias:
$hits"
  fi
done

# ── 4. Evidencia de verificación presente y fresca ────────────────────────────
# El teammate debe escribir, antes de completar, qué summary_id validó y el resultado HTTP real.
if [[ ! -f "$EVIDENCE_FILE" ]]; then
  fail "Falta evidencia de verificación en $EVIDENCE_FILE. Lección 6/11: 'listo' sin dato real persistido (summary_id + HTTP por gateway) no es completable. Registrá qué instancia validaste y el resultado, después marcá completa."
fi
if find "$EVIDENCE_FILE" -mmin +"$EVIDENCE_MAX_AGE_MIN" 2>/dev/null | grep -q .; then
  fail "La evidencia en $EVIDENCE_FILE tiene más de ${EVIDENCE_MAX_AGE_MIN} min — probablemente es de otra tarea. Re-verificá sobre la instancia persistida de ESTA tarea (Lección 6)."
fi

exit 0
