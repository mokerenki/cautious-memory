#!/usr/bin/env bash
# cautiousmemory cross-platform release builder.
#
# The default mode builds one target and embeds the already-exported frontend.
# `./build.sh --release` builds and packages all supported desktop/server targets.
#
# Environment variables:
#   CAUTIOUSMEMORY_TARGET_OS=linux             One target OS in single-target mode.
#   CAUTIOUSMEMORY_TARGET_ARCH=amd64           One target arch in single-target mode.
#   CAUTIOUSMEMORY_TARGETS=linux/amd64,...     Comma-separated targets for multi-target mode.
#   CAUTIOUSMEMORY_BUILD_VERSION=v0.3.3        Version embedded in the binary and archive name.
#   CAUTIOUSMEMORY_OUTPUT=/path/to/cautiousmemory       Explicit binary path in single-target mode.
#   CAUTIOUSMEMORY_OUTPUT_DIR=dist             Directory for default binary paths.
#   CAUTIOUSMEMORY_PACKAGE=1                   Create a zip archive for each target.
#   CAUTIOUSMEMORY_PACKAGE_DIR=dist            Directory for release archives.
#   CAUTIOUSMEMORY_COMPRESS=off                UPX mode: off, auto, or required.
#   CAUTIOUSMEMORY_UPX_ARGS="--best --lzma"    Arguments passed to UPX.
#   CAUTIOUSMEMORY_SKIP_FRONTEND=1             Reuse server/webui/dist (for CI artifact builds).
#   CAUTIOUSMEMORY_SKIP_NPM_CI=1               Skip npm ci while rebuilding the frontend.
#   CAUTIOUSMEMORY_GOSUMDB=sum.golang.org      Go checksum database.
set -euo pipefail

cd "$(cd "$(dirname "$0")" && pwd)"

info() { printf '\033[36m[*]\033[0m %s\n' "$*"; }
ok() { printf '\033[32m[+]\033[0m %s\n' "$*"; }
warn() { printf '\033[33m[!]\033[0m %s\n' "$*" >&2; }
die() { printf '\033[31m[x]\033[0m %s\n' "$*" >&2; exit 1; }

usage() {
  cat <<'EOF'
Usage:
  ./build.sh                         Build for the current operating system and architecture
  ./build.sh --target linux/amd64   Build for a specified target
  ./build.sh --release               Build and package all supported targets

Options:
  --release              Build amd64/arm64 targets for Linux, macOS, and Windows and create ZIP archives
  --target OS/ARCH       Set a single target, e.g. windows/amd64
  --upx                  Require UPX binary compression (may affect compatibility in some Linux environments)
  --no-compress          Skip UPX; only strip with the Go linker and compress the ZIP archive
  --help                 Show this help

Override the multi-target list with CAUTIOUSMEMORY_TARGETS, for example:
  CAUTIOUSMEMORY_TARGETS=linux/amd64,windows/amd64 ./build.sh --release
EOF
}

RELEASE_TARGETS_DEFAULT="linux/amd64,linux/arm64,darwin/amd64,darwin/arm64,windows/amd64"
CAUTIOUSMEMORY_RELEASE="${CAUTIOUSMEMORY_RELEASE:-0}"
CAUTIOUSMEMORY_COMPRESS="${CAUTIOUSMEMORY_COMPRESS:-off}"
CAUTIOUSMEMORY_PACKAGE="${CAUTIOUSMEMORY_PACKAGE:-0}"

while [ "$#" -gt 0 ]; do
  case "$1" in
    --release)
      CAUTIOUSMEMORY_RELEASE=1
      CAUTIOUSMEMORY_PACKAGE=1
      shift
      ;;
    --target)
      [ "$#" -ge 2 ] || die "--target requires an OS/ARCH argument"
      target_arg="$2"
      case "$target_arg" in
        */*)
          CAUTIOUSMEMORY_TARGET_OS="${target_arg%%/*}"
          CAUTIOUSMEMORY_TARGET_ARCH="${target_arg##*/}"
          CAUTIOUSMEMORY_TARGETS="$target_arg"
          ;;
        *) die "Target must be OS/ARCH, e.g. linux/amd64" ;;
      esac
      shift 2
      ;;
    --no-compress)
      CAUTIOUSMEMORY_COMPRESS=0
      shift
      ;;
    --upx)
      CAUTIOUSMEMORY_COMPRESS=required
      shift
      ;;
    --help|-h)
      usage
      exit 0
      ;;
    *) die "Unknown argument: $1 (use --help for usage)" ;;
  esac
done

command -v go >/dev/null 2>&1 || die "Go not found (this project requires Go 1.26 or later)"

CAUTIOUSMEMORY_GOSUMDB="${CAUTIOUSMEMORY_GOSUMDB:-sum.golang.org}"
if [ -z "${CAUTIOUSMEMORY_BUILD_VERSION:-}" ]; then
  if command -v git >/dev/null 2>&1 && git rev-parse --is-inside-work-tree >/dev/null 2>&1; then
    CAUTIOUSMEMORY_BUILD_VERSION="$(git describe --tags --always --dirty)"
  else
    CAUTIOUSMEMORY_BUILD_VERSION="dev"
  fi
fi
# Release tags are commonly passed as v0.3.3; keep the binary version consistent.
CAUTIOUSMEMORY_BUILD_VERSION="${CAUTIOUSMEMORY_BUILD_VERSION#v}"
CAUTIOUSMEMORY_OUTPUT_DIR="${CAUTIOUSMEMORY_OUTPUT_DIR:-dist}"
CAUTIOUSMEMORY_PACKAGE_DIR="${CAUTIOUSMEMORY_PACKAGE_DIR:-$CAUTIOUSMEMORY_OUTPUT_DIR}"
CAUTIOUSMEMORY_UPX_ARGS="${CAUTIOUSMEMORY_UPX_ARGS:---best --lzma}"

if [ "${CAUTIOUSMEMORY_RELEASE}" = "1" ]; then
  CAUTIOUSMEMORY_TARGETS="${CAUTIOUSMEMORY_TARGETS:-$RELEASE_TARGETS_DEFAULT}"
else
  CAUTIOUSMEMORY_TARGET_OS="${CAUTIOUSMEMORY_TARGET_OS:-$(GOSUMDB="$CAUTIOUSMEMORY_GOSUMDB" go env GOOS)}"
  CAUTIOUSMEMORY_TARGET_ARCH="${CAUTIOUSMEMORY_TARGET_ARCH:-$(GOSUMDB="$CAUTIOUSMEMORY_GOSUMDB" go env GOARCH)}"
  CAUTIOUSMEMORY_TARGETS="${CAUTIOUSMEMORY_TARGETS:-${CAUTIOUSMEMORY_TARGET_OS}/${CAUTIOUSMEMORY_TARGET_ARCH}}"
fi

if [ "${CAUTIOUSMEMORY_SKIP_FRONTEND:-0}" = "1" ]; then
  [ -d server/webui/dist ] || die "CAUTIOUSMEMORY_SKIP_FRONTEND=1 but server/webui/dist does not exist"
else
  command -v npm >/dev/null 2>&1 || die "npm not found (Node.js/npm is required to build the static frontend)"
  command -v rsync >/dev/null 2>&1 || die "rsync not found"
  info "Building static frontend assets"
  if [ "${CAUTIOUSMEMORY_SKIP_NPM_CI:-0}" != "1" ]; then
    (cd web && npm ci)
  fi
  (cd web && npm run build:static)
  info "Syncing frontend assets to server/webui/dist"
  mkdir -p server/webui/dist
  rsync -a --delete web/out/ server/webui/dist/
fi

compress_binary() {
  binary="$1"
  goos="$2"
  case "$CAUTIOUSMEMORY_COMPRESS" in
    0|off|false|none)
      info "Skipping UPX: $binary"
      return 0
      ;;
    auto|required|true|1) ;;
    *) die "CAUTIOUSMEMORY_COMPRESS must be off, auto, or required" ;;
  esac

  if ! command -v upx >/dev/null 2>&1; then
    if [ "$CAUTIOUSMEMORY_COMPRESS" = "required" ]; then
      die "CAUTIOUSMEMORY_COMPRESS=required but UPX was not found"
    fi
    warn "UPX not found; keeping the linker-compressed binary: $binary"
    return 0
  fi

  before=$(wc -c < "$binary" | tr -d ' ')
  upx_args="$CAUTIOUSMEMORY_UPX_ARGS"
  [ "$goos" = "darwin" ] && upx_args="$upx_args --force-macos"
  # shellcheck disable=SC2086
  if ! upx $upx_args -- "$binary"; then
    if [ "$CAUTIOUSMEMORY_COMPRESS" = "required" ]; then
      die "UPX compression failed: $binary"
    fi
    warn "UPX does not support this target format; keeping the uncompressed binary: $binary"
    return 0
  fi
  after=$(wc -c < "$binary" | tr -d ' ')
  ok "UPX compression complete: $binary (${before} -> ${after} bytes)"
}

package_binary() {
  binary="$1"
  goos="$2"
  goarch="$3"
  package_name="cautiousmemory-${CAUTIOUSMEMORY_BUILD_VERSION}-${goos}-${goarch}"
  package_root="${CAUTIOUSMEMORY_PACKAGE_DIR}/${package_name}"
  archive="${CAUTIOUSMEMORY_PACKAGE_DIR}/${package_name}.zip"

  command -v zip >/dev/null 2>&1 || die "zip is required for packaging"
  rm -rf "$package_root" "$archive"
  mkdir -p "$package_root"
  cp "$binary" "$package_root/"
  # The supervisor startup script is the official entry point: one-click updates on the page rely on it to relaunch the process after it exits,
  # because running cautiousmemory directly means it will not restart after an update. Include only the script for the target operating system.
  if [ "$goos" = "windows" ]; then
    cp start.bat "$package_root/"
  else
    cp start.sh "$package_root/"
    chmod +x "$package_root/start.sh"
  fi
  cp -R skills "$package_root/"
  cp config.example.json "$package_root/"
  if [ -f README.md ]; then cp README.md "$package_root/"; fi
  (cd "$CAUTIOUSMEMORY_PACKAGE_DIR" && zip -q -r -9 "$(basename "$archive")" "$(basename "$package_root")")
  rm -rf "$package_root"
  ok "Release archive: $archive"
}

build_target() {
  target="$1"
  case "$target" in
    */*) ;;
    *) die "Invalid target: $target (must be OS/ARCH)" ;;
  esac
  goos="${target%%/*}"
  goarch="${target##*/}"
  case "$goos" in
    linux|darwin|windows) ;;
    *) die "Unsupported operating system: $goos (supported: linux, darwin, windows)" ;;
  esac

  binary_name="cautiousmemory"
  [ "$goos" = "windows" ] && binary_name="cautiousmemory.exe"
  if [ -n "${CAUTIOUSMEMORY_OUTPUT:-}" ] && [ "$CAUTIOUSMEMORY_RELEASE" != "1" ]; then
    output="$CAUTIOUSMEMORY_OUTPUT"
  else
    output="${CAUTIOUSMEMORY_OUTPUT_DIR}/cautiousmemory-${goos}-${goarch}/${binary_name}"
  fi
  mkdir -p "$(dirname "$output")"

  info "Building ${goos}/${goarch}, version ${CAUTIOUSMEMORY_BUILD_VERSION}"
  GOSUMDB="$CAUTIOUSMEMORY_GOSUMDB" \
  CGO_ENABLED=0 \
  GOOS="$goos" \
  GOARCH="$goarch" \
  go build \
    -tags embedui \
    -trimpath \
    -ldflags "-s -w -buildid= -X main.version=${CAUTIOUSMEMORY_BUILD_VERSION}" \
    -o "$output" \
    ./cmd/cautiousmemory

  compress_binary "$output" "$goos"
  if command -v file >/dev/null 2>&1; then file "$output"; fi
  if [ "$CAUTIOUSMEMORY_PACKAGE" = "1" ]; then package_binary "$output" "$goos" "$goarch"; fi
  ok "Build complete: $output"
}

write_checksums() {
  [ "$CAUTIOUSMEMORY_PACKAGE" = "1" ] || return 0
  checksum_file="$CAUTIOUSMEMORY_PACKAGE_DIR/SHA256SUMS"
  if command -v sha256sum >/dev/null 2>&1; then
    (cd "$CAUTIOUSMEMORY_PACKAGE_DIR" && for archive in *.zip; do sha256sum "$archive"; done > "$(basename "$checksum_file")")
  elif command -v shasum >/dev/null 2>&1; then
    (cd "$CAUTIOUSMEMORY_PACKAGE_DIR" && for archive in *.zip; do shasum -a 256 "$archive"; done > "$(basename "$checksum_file")")
  else
    warn "Neither sha256sum nor shasum found; skipping SHA256SUMS"
    return 0
  fi
  ok "Checksum file: $checksum_file"
}

mkdir -p "$CAUTIOUSMEMORY_OUTPUT_DIR"
if [ "$CAUTIOUSMEMORY_PACKAGE" = "1" ]; then mkdir -p "$CAUTIOUSMEMORY_PACKAGE_DIR"; fi

old_ifs="$IFS"
IFS=','
read -r -a targets <<< "$CAUTIOUSMEMORY_TARGETS"
IFS="$old_ifs"
[ "${#targets[@]}" -gt 0 ] || die "CAUTIOUSMEMORY_TARGETS must not be empty"
for target in "${targets[@]}"; do
  target="${target//[[:space:]]/}"
  [ -n "$target" ] || continue
  build_target "$target"
done

if [ "$CAUTIOUSMEMORY_PACKAGE" = "1" ]; then
  write_checksums
  info "Release packages created in: $CAUTIOUSMEMORY_PACKAGE_DIR"
fi

