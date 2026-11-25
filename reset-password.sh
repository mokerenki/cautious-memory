#!/usr/bin/env bash
# =============================================================================
# cautiousmemory administrator password reset script
#
# The login username is fixed as cautiousmemory; the password is stored as a bcrypt hash in the database settings table under
# auth.password_hash. This script connects to the database and uses pgcrypto to generate a bcrypt hash there,
# then writes it to that keyâ€”fully compatible with backend login verification (golang.org/x/crypto/bcrypt).
#
# Two deployment modes:
#   local (default) â€” connect directly to the database from the host with psql. Connection details are resolved in this order:
#                    command-line arguments > --dsn/$CAUTIOUSMEMORY_PG_DSN > database.* in config.json
#   docker        â€” run psql inside the postgres container using `docker compose exec` (or `docker exec`)
#                    (Compose does not expose port 5432 to the host by default, so psql runs inside the container).
#
# Usage examples:
#   ./reset-password.sh                          # local; read config.json/environment automatically and prompt for a new password
#   ./reset-password.sh -p 'NewPass!'            # local; provide the new password directly
#   ./reset-password.sh --dsn postgres://u:p@h:5432/cautiousmemory
#   ./reset-password.sh -H 127.0.0.1 -P 5433 -U autopentest -W pass -d cautiousmemory
#   ./reset-password.sh -m docker                # Docker deployment (read POSTGRES_* from .env)
#   ./reset-password.sh -m docker -c postgres-container-name --exec docker
#
# Security: the new password is passed through an environment variable and psql \getenv (not process argv), and :'var' is
# escaped automatically (to prevent SQL injection); the database password is passed via PGPASSWORD and likewise does not appear in argv.
# =============================================================================
set -euo pipefail

PASS_KEY="auth.password_hash"
BCRYPT_COST=10

MODE=""            # local | docker (empty = automatic detection)
DSN=""
HOST="" PORT="" USER="" DBPASS="" DBNAME="" SSLMODE=""
CONFIG=""
CONTAINER=""       # postgres service/container name in Docker mode (default: postgres)
EXEC_KIND=""       # compose | docker (which exec method to use in Docker mode; empty = automatic)
NEWPASS=""
ASSUME_YES=0

die() { echo "Error: $*" >&2; exit 1; }
info() { echo "Â· $*" >&2; }

usage() { sed -n '2,40p' "$0" | sed 's/^# \{0,1\}//'; exit 0; }

# ---- Parse arguments ------------------------------------------------------
while [[ $# -gt 0 ]]; do
  case "$1" in
    -m|--mode)        MODE="${2:-}"; shift 2 ;;
    --dsn)            DSN="${2:-}"; shift 2 ;;
    -H|--host)        HOST="${2:-}"; shift 2 ;;
    -P|--port)        PORT="${2:-}"; shift 2 ;;
    -U|--user)        USER="${2:-}"; shift 2 ;;
    -W|--db-password) DBPASS="${2:-}"; shift 2 ;;
    -d|--dbname)      DBNAME="${2:-}"; shift 2 ;;
    --sslmode)        SSLMODE="${2:-}"; shift 2 ;;
    --config)         CONFIG="${2:-}"; shift 2 ;;
    -c|--container)   CONTAINER="${2:-}"; shift 2 ;;
    --exec)           EXEC_KIND="${2:-}"; shift 2 ;;
    -p|--new-password) NEWPASS="${2:-}"; shift 2 ;;
    -y|--yes)         ASSUME_YES=1; shift ;;
    -h|--help)        usage ;;
    *) die "Unknown argument: $1 (use -h for usage)" ;;
  esac
done

# ---- Read database.* from config.json (local mode only, when connection details were not explicitly provided)-----
# Prefer parsing with python3 (more robust); fall back to grep if python3 is unavailable (config.json uses regular separate fields).
read_config_json() {
  local path="$1"
  [[ -f "$path" ]] || return 1
  if command -v python3 >/dev/null 2>&1; then
    python3 - "$path" <<'PY'
import json, sys
try:
    d = json.load(open(sys.argv[1])).get("database", {})
except Exception:
    sys.exit(1)
# Accept a DSN directly or separate fields
if d.get("dsn"):
    print("DSN\t" + d["dsn"]); sys.exit(0)
for k in ("host","port","user","password","dbname","sslmode"):
    if d.get(k) is not None:
        print(k.upper() + "\t" + str(d[k]))
PY
  else
    # Minimal fallback: grep each key (values may be strings or numbers)
    local k
    for k in host port user password dbname sslmode; do
      local v
      v=$(grep -oE "\"$k\"[[:space:]]*:[[:space:]]*(\"[^\"]*\"|[0-9]+)" "$path" 2>/dev/null \
            | head -1 | sed -E "s/.*:[[:space:]]*//; s/^\"//; s/\"$//") || true
      [[ -n "$v" ]] && echo -e "${k^^}\t$v"
    done
  fi
}

apply_config_fields() {
  local line key val
  while IFS=$'\t' read -r key val; do
    [[ -z "$key" ]] && continue
    case "$key" in
      DSN)      [[ -z "$DSN" ]] && DSN="$val" ;;
      HOST)     [[ -z "$HOST" ]] && HOST="$val" ;;
      PORT)     [[ -z "$PORT" ]] && PORT="$val" ;;
      USER)     [[ -z "$USER" ]] && USER="$val" ;;
      PASSWORD) [[ -z "$DBPASS" ]] && DBPASS="$val" ;;
      DBNAME)   [[ -z "$DBNAME" ]] && DBNAME="$val" ;;
      SSLMODE)  [[ -z "$SSLMODE" ]] && SSLMODE="$val" ;;
    esac
  done
}

# ---- Automatically determine the mode ---------------------------------------------------------
if [[ -z "$MODE" ]]; then
  if [[ -n "$DSN$HOST$USER$DBNAME" || -n "${CAUTIOUSMEMORY_PG_DSN:-}" || -f "${CONFIG:-config.json}" ]]; then
    MODE="local"
  elif command -v docker >/dev/null 2>&1 && [[ -f docker-compose.yml ]]; then
    MODE="docker"
  else
    MODE="local"
  fi
fi
info "Deployment mode: $MODE"

# ---- Collect the new password -----------------------------------------------------------
if [[ -z "$NEWPASS" ]]; then
  read -r -s -p "Enter a new password (username is fixed as cautiousmemory): " NEWPASS; echo >&2
  [[ -n "$NEWPASS" ]] || die "Password cannot be empty"
  read -r -s -p "Enter it again to confirm: " NEWPASS2; echo >&2
  [[ "$NEWPASS" == "$NEWPASS2" ]] || die "The passwords do not match"
fi
[[ -n "$NEWPASS" ]] || die "Password cannot be empty"

# Pass the password to psql through an environment variable (read with \getenv; not included in argv/ps)
export CAUTIOUSMEMORY_RESET_NEWPASS="$NEWPASS"

# Generate bcrypt in the database and upsert it; the password is escaped automatically with :'newpw'. CREATE EXTENSION is idempotent,
# and this fails if the database role lacks permission to create extensions (see the run failure message below).
SQL=$(cat <<SQL
\\set ON_ERROR_STOP on
\\getenv newpw CAUTIOUSMEMORY_RESET_NEWPASS
CREATE EXTENSION IF NOT EXISTS pgcrypto;
INSERT INTO settings(key, value)
VALUES ('$PASS_KEY', crypt(:'newpw', gen_salt('bf', $BCRYPT_COST)))
ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value, updated_at = now();
SQL
)

# ---- Execute -----------------------------------------------------------------
if [[ "$MODE" == "local" ]]; then
  # Connection detail priority: command line > --dsn/$CAUTIOUSMEMORY_PG_DSN > config.json
  if [[ -z "$DSN" && -z "$HOST$USER$DBNAME" ]]; then
    [[ -n "${CAUTIOUSMEMORY_PG_DSN:-}" ]] && DSN="$CAUTIOUSMEMORY_PG_DSN"
  fi
  if [[ -z "$DSN" && -z "$HOST$USER$DBNAME" ]]; then
    cfg="${CONFIG:-config.json}"
    if [[ -f "$cfg" ]]; then
      info "Reading database configuration from $cfg"
      apply_config_fields < <(read_config_json "$cfg")
    fi
  fi

  command -v psql >/dev/null 2>&1 || die "psql not found on this machine (install postgresql-client or use -m docker)"

  declare -a PSQL_ARGS=()
  if [[ -n "$DSN" ]]; then
    PSQL_ARGS=("$DSN")
    target="$DSN"
  else
    [[ -n "$USER"   ]] || die "Missing database user (-U) or a valid config.json/DSN"
    [[ -n "$DBNAME" ]] || die "Missing database name (-d) or a valid config.json/DSN"
    HOST="${HOST:-127.0.0.1}"; PORT="${PORT:-5432}"; SSLMODE="${SSLMODE:-disable}"
    PSQL_ARGS=(-h "$HOST" -p "$PORT" -U "$USER" -d "$DBNAME")
    [[ -n "$SSLMODE" ]] && export PGSSLMODE="$SSLMODE"
    [[ -n "$DBPASS" ]] && export PGPASSWORD="$DBPASS"
    target="$USER@$HOST:$PORT/$DBNAME"
  fi

  info "Target database: $target"
  if [[ "$ASSUME_YES" -ne 1 ]]; then
    read -r -p "Reset the cautiousmemory password in this database? [y/N] " ans
    [[ "$ans" == "y" || "$ans" == "Y" ]] || die "Cancelled"
  fi

  if ! printf '%s\n' "$SQL" | psql "${PSQL_ARGS[@]}" -v ON_ERROR_STOP=1 -q >/dev/null; then
    die "Write failed. If the error mentions missing pgcrypto or permissions, use a role that can create extensions or run CREATE EXTENSION pgcrypto manually first."
  fi

else
  # ---- docker ----
  command -v docker >/dev/null 2>&1 || die "docker not found"
  CONTAINER="${CONTAINER:-postgres}"

  # Choose the exec method: prefer docker compose exec (service name), otherwise use docker exec (container name)
  if [[ -z "$EXEC_KIND" ]]; then
    if docker compose version >/dev/null 2>&1 && [[ -f docker-compose.yml ]]; then
      EXEC_KIND="compose"
    else
      EXEC_KIND="docker"
    fi
  fi

  # psql credentials inside the container: prefer command-line values, then POSTGRES_* from .env, then the Compose default (cautiousmemory)
  if [[ -f .env ]]; then
    # shellcheck disable=SC1091
    set -a; . ./.env; set +a
  fi
  DUSER="${USER:-${POSTGRES_USER:-cautiousmemory}}"
  DNAME="${DBNAME:-${POSTGRES_DB:-cautiousmemory}}"
  [[ -n "$DBPASS" ]] && export PGPASSWORD="$DBPASS"
  [[ -z "${PGPASSWORD:-}" && -n "${POSTGRES_PASSWORD:-}" ]] && export PGPASSWORD="$POSTGRES_PASSWORD"

  info "Target: psql -U $DUSER -d $DNAME inside container $CONTAINER (exec=$EXEC_KIND)"
  if [[ "$ASSUME_YES" -ne 1 ]]; then
    read -r -p "Reset the cautiousmemory password in this container database? [y/N] " ans
    [[ "$ans" == "y" || "$ans" == "Y" ]] || die "Cancelled"
  fi

  # -e passes only variable names, not values â†’ inherit them from the current environment; the password does not appear in docker command argv.
  declare -a EXEC_CMD
  if [[ "$EXEC_KIND" == "compose" ]]; then
    EXEC_CMD=(docker compose exec -T -e CAUTIOUSMEMORY_RESET_NEWPASS -e PGPASSWORD "$CONTAINER"
              psql -U "$DUSER" -d "$DNAME" -v ON_ERROR_STOP=1 -q)
  else
    EXEC_CMD=(docker exec -i -e CAUTIOUSMEMORY_RESET_NEWPASS -e PGPASSWORD "$CONTAINER"
              psql -U "$DUSER" -d "$DNAME" -v ON_ERROR_STOP=1 -q)
  fi

  if ! printf '%s\n' "$SQL" | "${EXEC_CMD[@]}" >/dev/null; then
    die "Write failed. Check the container name (-c), database account (POSTGRES_* in .env), and whether the role has pgcrypto privileges."
  fi
fi

unset CAUTIOUSMEMORY_RESET_NEWPASS
echo "âœ“ cautiousmemory administrator password reset. Sign in with username cautiousmemory and the new password (no service restart required)."

