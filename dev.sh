#!/usr/bin/env bash
# Development mode: run the backend (:8787), traffic proxy (:8788), and frontend next dev (:5173) together.
# The frontend proxies /api to the backend; Ctrl-C stops all processes.
#
# For the single-binary (embedded frontend) approach, see the "Single binary" section in README; this script is not used.
set -euo pipefail
cd "$(dirname "$0")"

# On exit, terminate all child processes in this process group (backend + frontend).
cleanup() { kill 0 2>/dev/null || true; }
trap cleanup EXIT INT TERM

# Backend (standard go run, without embedded frontend); configure the number of concurrent work agents in "System Settings".
go run ./cmd/cautiousmemory -addr :8787 -proxy 127.0.0.1:8788 &

# Frontend hot reload (Vite/Next dev server, with /api proxied to :8787).
( cd web && npm run dev ) &

echo "[dev] backend :8787 / proxy :8788 / frontend http://localhost:5173  (Ctrl-C to exit)"
wait

