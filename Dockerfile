# syntax=docker/dockerfile:1
#
# Runtime image (no compilation in the image): install common tools and copy in the **precompiled Linux binary**.
# The CI binaries job cross-compiles the binary (pure Go, no QEMU) and places it by target architecture in
# dist/<TARGETARCH>/artex in the build context. Multi-architecture builds then emulate only the apt layer for arm64,
# rather than emulating Next/Go compilation, which is much faster.
#
# To build the image locally by hand, prepare the binary first:
#   cd web && npm run build:static && cd ..
#   cp -r web/out server/webui/dist
#   CGO_ENABLED=0 GOARCH=amd64 go build -tags embedui -o dist/amd64/artex ./cmd/artex
#   docker build -t artex:local .
FROM python:3.12-slim-bookworm
ARG TARGETARCH
# Common tools: ripgrep / curl / vim, plus standard recon utilities (add or remove as needed).
# Install Node 22.x from NodeSource to match the major version used for frontend builds.
RUN apt-get update && apt-get install -y --no-install-recommends \
      ca-certificates ripgrep curl wget vim git jq unzip \
      tzdata \
      dnsutils iputils-ping netcat-openbsd inetutils-telnet whois nmap \
    && curl -fsSL https://deb.nodesource.com/setup_22.x | bash - \
    && apt-get install -y --no-install-recommends nodejs \
    && rm -rf /var/lib/apt/lists/*
# Install Playwright MCP and CLI globally so npx does not download them at runtime.
# @playwright/mcp: start the browser MCP with `npx @playwright/mcp` (installed globally; no -y/@latest needed).
# @playwright/cli: provides playwright-cli; run --help after installation to verify it is executable.
# Install playwright as well (for browser management), then use --with-deps to install Chromium and its system dependencies,
# so the MCP/CLI can run on first launch in the container without downloading a browser.
RUN npm install -g @playwright/mcp@latest @playwright/cli@latest playwright@latest \
    && playwright-cli --help \
    && playwright install --with-deps chromium \
    && rm -rf /var/lib/apt/lists/*
WORKDIR /app
ENV TZ=Asia/Shanghai
# Precompiled binary for the corresponding architecture (dist/amd64/artex or dist/arm64/artex)
COPY dist/${TARGETARCH}/artex /app/artex
# Supervisor startup script: restarts the process according to its exit code; the UI’s one-click updater relies on it to swap binaries.
# It also forwards SIGTERM to artex — docker stop sends the signal only to PID 1,
# so without forwarding, artex cannot shut down gracefully and is force-killed with SIGKILL after 10 seconds.
COPY start.sh /app/start.sh
RUN chmod +x /app/artex /app/start.sh
COPY skills/ /app/skills/
# Persistence location for data/ (SQLite + jwt.key)
VOLUME ["/app/data"]
EXPOSE 8787 8788
ENTRYPOINT ["/app/start.sh"]
CMD ["-addr", ":8787", "-proxy", ":8788"]
