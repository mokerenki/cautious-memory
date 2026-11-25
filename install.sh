#!/usr/bin/env bash
# cautiousmemory installation script: â‘  Docker-based installation â‘¡ build and run locally
set -euo pipefail
cd "$(cd "$(dirname "$0")" && pwd)"

info(){ printf '\033[36m[*]\033[0m %s\n' "$*"; }
ok(){   printf '\033[32m[+]\033[0m %s\n' "$*"; }
warn(){ printf '\033[33m[!]\033[0m %s\n' "$*"; }
die(){  printf '\033[31m[x]\033[0m %s\n' "$*" >&2; exit 1; }
ask(){  local p="$1" d="${2:-}" a; read -rp "$p${d:+ [$d]}: " a; echo "${a:-$d}"; }
rand(){ head -c 18 /dev/urandom | base64 | tr -dc 'A-Za-z0-9' | head -c 24; }

# â”€â”€ Docker environment check / auto-installation â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€
ensure_docker(){
  if command -v docker >/dev/null 2>&1 && docker compose version >/dev/null 2>&1; then
    ok "Docker and Docker Compose detected"; return
  fi
  warn "Docker / Docker Compose not found"
  case "$(uname -s)" in
    Linux)
      if [ "$(ask 'Install Docker automatically? (y/n)' y)" = y ]; then
        curl -fsSL https://get.docker.com | sh
        sudo usermod -aG docker "$USER" || true
        ok "Docker installed (log out and back in for the group change to take effect and run Docker without sudo)"
      else
        die "Please install Docker manually, then try again"
      fi ;;
    Darwin) die "On macOS, install Docker Desktop: https://www.docker.com/products/docker-desktop/" ;;
    *)      die "Please install Docker manually, then try again" ;;
  esac
}

# â”€â”€ â‘  Docker-based installation â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€
install_docker(){
  ensure_docker
  umask 077
  if [ ! -f .env ]; then
    cp .env.example .env 2>/dev/null || true
    local pw key
    pw="$(ask 'Postgres password (press Enter to generate a random one)' "$(rand)")"
    key="$(ask 'ANTHROPIC_API_KEY (optional; configure it later in the UI)' '')"
    sed -i.bak "s|^POSTGRES_PASSWORD=.*|POSTGRES_PASSWORD=${pw}|" .env
    sed -i.bak "s|^ANTHROPIC_API_KEY=.*|ANTHROPIC_API_KEY=${key}|" .env
    rm -f .env.bak
    ok ".env created (POSTGRES_PASSWORD has been set)"
  else
    info "Using the existing .env"
  fi
  info "Pulling images and startingâ€¦"
  docker compose pull
  docker compose up -d
  ok "Started â†’ http://localhost:8787"
  info "View logs: docker compose logs -f cautiousmemory"
}

# â”€â”€ â‘¡ Build and run locally â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€
install_local(){
  echo "Database setup:"
  echo "  1) Connect to an existing PostgreSQL instance"
  echo "  2) Start PostgreSQL with Docker (Docker required)"
  case "$(ask 'Select an option' 1)" in
    2)
      ensure_docker
      local pw; pw="$(ask 'Postgres password (press Enter for a random one)' "$(rand)")"
      docker run -d --name cautiousmemory-pg -p 5432:5432 \
        -e POSTGRES_USER=cautiousmemory -e POSTGRES_PASSWORD="$pw" -e POSTGRES_DB=cautiousmemory \
        -v cautiousmemory-pg:/var/lib/postgresql/data postgres:16-alpine
      DB_HOST=127.0.0.1 DB_PORT=5432 DB_USER=cautiousmemory DB_PASS="$pw" DB_NAME=cautiousmemory DB_SSL=disable ;;
    *)
      DB_HOST="$(ask 'Database host' 127.0.0.1)"
      DB_PORT="$(ask 'Port' 5432)"
      DB_USER="$(ask 'Username' cautiousmemory)"
      DB_PASS="$(ask 'Password' '')"
      DB_NAME="$(ask 'Database name' cautiousmemory)"
      DB_SSL="$(ask 'sslmode (disable/require)' disable)" ;;
  esac

  # Generate config.json
  cat > config.json <<JSON
{
  "database": {
    "host": "${DB_HOST}",
    "port": ${DB_PORT},
    "user": "${DB_USER}",
    "password": "${DB_PASS}",
    "dbname": "${DB_NAME}",
    "sslmode": "${DB_SSL}"
  }
}
JSON
  ok "config.json created"

  # Go environment check
  command -v go >/dev/null 2>&1 || die "Go not found; install Go 1.26 or later: https://go.dev/dl/"
  ok "Go: $(go version)"

  # The embedded frontend requires Node.js to generate static assets
  if command -v npm >/dev/null 2>&1; then
    info "Building static frontend assetsâ€¦"
    ( cd web && npm ci && npm run build:static )
    rm -rf server/webui/dist && cp -r web/out server/webui/dist
    info "Compiling the single binary with embedded frontendâ€¦"
    CGO_ENABLED=0 go build -tags embedui -trimpath -o cautiousmemory ./cmd/cautiousmemory
  else
    warn "npm not found: building the backend **without the embedded frontend** (run the frontend separately with npm run dev)"
    CGO_ENABLED=0 go build -o cautiousmemory ./cmd/cautiousmemory
  fi
  ok "Build complete â†’ ./cautiousmemory"

  info "Startingâ€¦ (press Ctrl-C to exit)"
  ./cautiousmemory
}

echo "=============================="
echo "  cautiousmemory installation"
echo "  1) Docker-based installation"
echo "  2) Run locally (compiled with Go)"
echo "=============================="
case "$(ask 'Select an option' 1)" in
  1) install_docker ;;
  2) install_local ;;
  *) die "Invalid selection" ;;
esac

