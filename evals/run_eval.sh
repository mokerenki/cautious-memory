#!/usr/bin/env bash
# Start the eval range and print an ARTEX task payload. Does NOT start ARTEX.
set -euo pipefail
ROOT="$(cd "$(dirname "$0")/.." && pwd)"
cd "$ROOT"

PORT="${EVAL_RANGE_PORT:-5050}"

echo "[evals] building/starting eval-range (profile eval)..."
docker compose --profile eval up -d --build eval-range

echo "[evals] waiting for health on :${PORT}..."
for i in $(seq 1 60); do
  if curl -sf "http://127.0.0.1:${PORT}/health" >/dev/null 2>&1; then
    echo "[evals] range is healthy"
    break
  fi
  if [[ "$i" -eq 60 ]]; then
    echo "[evals] timeout waiting for range" >&2
    exit 1
  fi
  sleep 1
done

TARGET="http://127.0.0.1:${PORT}"
cat <<EOF

========== ARTEX task payload (paste into task create) ==========
Target: ${TARGET}

Suggested goal:
Authorized security assessment of the eval-range business application at ${TARGET}.
Focus on business-logic flaws: IDOR/BOLA on documents, checkout price integrity,
single-use coupon atomicity, refund workflow enforcement, and free-tier project quotas.
Use accounts alice/alice and bob/bob (users) and staff/staff when role checks matter.
Do not DoS; race tests ≤5 rps. Report findings with clear PoC and evidence.

Health: ${TARGET}/health
Ground truth (for offline scoring only): evals/range/groundtruth.json
=================================================================

EOF
