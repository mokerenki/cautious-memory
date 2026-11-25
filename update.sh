#!/usr/bin/env bash
# cautiousmemory update script: â‘  Docker update (pull and recreate the image) â‘¡ local build update (rebuild the binary)
# Counterpart to install.sh: install performs the initial setup; update upgrades to a new version.
# No manual DB migration is neededâ€”cautiousmemory reruns schema.sql idempotently on every startup (including ADD COLUMN/CREATE
# INDEX IF NOT EXISTS), so restarting applies migrations. Data (the pgdata volume, ./data, and ./skills) is unaffected.
set -euo pipefail
cd "$(cd "$(dirname "$0")" && pwd)"

info(){ printf '\033[36m[*]\033[0m %s\n' "$*"; }
ok(){   printf '\033[32m[+]\033[0m %s\n' "$*"; }
warn(){ printf '\033[33m[!]\033[0m %s\n' "$*"; }
die(){  printf '\033[31m[x]\033[0m %s\n' "$*" >&2; exit 1; }
ask(){  local p="$1" d="${2:-}" a; read -rp "$p${d:+ [$d]}: " a; echo "${a:-$d}"; }

# â”€â”€ Optional: sync the repository to the latest code (Compose files, scripts, and local-build source are updated this way)â”€â”€â”€â”€â”€â”€â”€
sync_repo(){
  [ -d .git ] && command -v git >/dev/null 2>&1 || { warn "Not a Git working copy; skipping git pull"; return; }
  [ "$(ask 'Pull the latest code (git pull --ff-only)? (y/n)' y)" = y ] || return
  if ! git pull --ff-only; then
    warn "git pull could not fast-forward (local changes or diverged branches); resolve this manually and try again. Using the current code for this run."
  fi
}

# â”€â”€ â‘  Docker update â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€
update_docker(){
  command -v docker >/dev/null 2>&1 && docker compose version >/dev/null 2>&1 \
    || die "Docker / Docker Compose not found; deploy first with ./install.sh"
  [ -f .env ] || die ".env not found; run ./install.sh to complete the initial deployment"

  # Optional: upgrade to a specific image tag (leave blank to keep CAUTIOUSMEMORY_TAG from .env; defaults to latest)
  local tag; tag="$(ask 'Target image tag (press Enter to keep the .env value / latest)' '')"
  if [ -n "$tag" ]; then
    if grep -q '^CAUTIOUSMEMORY_TAG=' .env; then
      sed -i.bak "s|^CAUTIOUSMEMORY_TAG=.*|CAUTIOUSMEMORY_TAG=${tag}|" .env && rm -f .env.bak
    else
      printf '\nCAUTIOUSMEMORY_TAG=%s\n' "$tag" >> .env
    fi
    ok "CAUTIOUSMEMORY_TAG set to ${tag}"
  fi

  # Only cautiousmemory is updated: postgres is fixed at 16-alpine and does not need an upgrade (pulling it would waste bandwidth,
  # and major-version changes could introduce compatibility risks). cautiousmemory declares depends_on postgres, so specifying the service name with
  # up starts postgres if it is down; if it is already running, it is left untouched and not recreated.
  info "Pulling the new image (cautiousmemory only)â€¦"
  docker compose pull cautiousmemory
  info "Recreating and starting (schema migrates automatically when cautiousmemory restarts)â€¦"
  docker compose up -d cautiousmemory
  ok "Update complete â†’ http://localhost:8787"
  info "View logs: docker compose logs -f cautiousmemory"
  info "Remove old images (optional): docker image prune -f"
}

# â”€â”€ â‘¡ Local build update â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€
update_local(){
  command -v go >/dev/null 2>&1 || die "Go 1.26 or later not found: https://go.dev/dl/"
  [ -f config.json ] || warn "config.json not foundâ€”use ./install.sh for the initial deployment"
  ok "Go: $(go version)"

  if command -v npm >/dev/null 2>&1; then
    info "Rebuilding static frontend assetsâ€¦"
    ( cd web && npm ci && npm run build:static )
    rm -rf server/webui/dist && cp -r web/out server/webui/dist
    info "Recompiling the single binary with embedded frontendâ€¦"
    CGO_ENABLED=0 go build -tags embedui -trimpath -o cautiousmemory ./cmd/cautiousmemory
  else
    warn "npm not found: compiling the backend **without the embedded frontend** (run the frontend separately with npm run dev)"
    CGO_ENABLED=0 go build -o cautiousmemory ./cmd/cautiousmemory
  fi
  ok "Build complete â†’ ./cautiousmemory"
  warn "Restart the running cautiousmemory process to apply the update (schema migrates automatically on restart)"
}

echo "=============================="
echo "  cautiousmemory update"
echo "  1) Docker update (pull and recreate the image)"
echo "  2) Local update (recompile with Go)"
echo "=============================="
case "$(ask 'Select an option' 1)" in
  1) sync_repo; update_docker ;;
  2) sync_repo; update_local ;;
  *) die "Invalid selection" ;;
esac

