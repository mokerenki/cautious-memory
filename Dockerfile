# syntax=docker/dockerfile:1
#
# è¿è¡Œé•œåƒï¼ˆä¸åœ¨é•œåƒé‡Œç¼–è¯‘ï¼‰ï¼šåªè£…å¸¸ç”¨å·¥å…·ï¼Œæ”¾å…¥**é¢„ç¼–è¯‘å¥½çš„ Linux å•äºŒè¿›åˆ¶**ã€‚
# äºŒè¿›åˆ¶ç”± CI çš„ binaries job äº¤å‰ç¼–è¯‘ï¼ˆçº¯ Goã€æ—  QEMUï¼‰ï¼ŒæŒ‰ç›®æ ‡æž¶æž„æ”¾åœ¨
# æž„å»ºä¸Šä¸‹æ–‡çš„ dist/<TARGETARCH>/cautiousmemoryã€‚è¿™æ ·å¤šæž¶æž„æž„å»ºæ—¶ arm64 åªéœ€æ¨¡æ‹Ÿ apt å±‚ï¼Œ
# ä¸å†æ¨¡æ‹Ÿ Next/Go ç¼–è¯‘ï¼Œé€Ÿåº¦å¿«å¾—å¤šã€‚
#
# æœ¬åœ°æ‰‹åŠ¨æž„å»ºé•œåƒæ—¶ï¼Œå…ˆè‡ªè¡Œå‡†å¤‡äºŒè¿›åˆ¶ï¼š
#   cd web && npm run build:static && cd ..
#   cp -r web/out server/webui/dist
#   CGO_ENABLED=0 GOARCH=amd64 go build -tags embedui -o dist/amd64/cautiousmemory ./cmd/cautiousmemory
#   docker build -t cautiousmemory:local .
FROM python:3.12-slim-bookworm
ARG TARGETARCH
# å¸¸ç”¨å·¥å…·ï¼šripgrep / curl / vimï¼ŒåŠ ä¸€æ‰¹ recon å¸¸å¤‡ä»¶ï¼ˆæŒ‰éœ€å¢žåˆ ï¼‰ã€‚
# Node ä»Ž NodeSource è£… 22.xï¼Œä¸Žå‰ç«¯æž„å»ºä½¿ç”¨çš„ä¸»ç‰ˆæœ¬ä¸€è‡´ã€‚
RUN apt-get update && apt-get install -y --no-install-recommends \
      ca-certificates ripgrep curl wget vim git jq unzip \
      tzdata \
      dnsutils iputils-ping netcat-openbsd inetutils-telnet whois nmap \
    && curl -fsSL https://deb.nodesource.com/setup_22.x | bash - \
    && apt-get install -y --no-install-recommends nodejs \
    && rm -rf /var/lib/apt/lists/*
# é¢„è£… Playwright MCP ä¸Ž CLIï¼ˆå…¨å±€ï¼‰ï¼Œè¿è¡Œæ—¶ä¸å† npx è”ç½‘ä¸‹è½½ã€‚
# @playwright/mcpï¼šbrowser MCP ç›´æŽ¥ `npx @playwright/mcp`ï¼ˆå·²å…¨å±€è£…å¥½ï¼Œæ— éœ€ -y/@latestï¼‰ã€‚
# @playwright/cliï¼šæä¾› playwright-cliï¼Œè£…å®Œé¡ºå¸¦ --help éªŒè¯å¯æ‰§è¡Œã€‚
# å†è£… playwrightï¼ˆæä¾›æµè§ˆå™¨ç®¡ç†ï¼‰ï¼Œè£…å®Œç”¨ --with-deps é¢„ç½® chromium åŠå…¶ç³»ç»Ÿä¾èµ–ï¼Œ
# è¿™æ ·å®¹å™¨å†… MCP/CLI é¦–æ¬¡å¯åŠ¨å³å¯ç”¨ï¼Œä¸å†è”ç½‘ä¸‹è½½æµè§ˆå™¨ã€‚
RUN npm install -g @playwright/mcp@latest @playwright/cli@latest playwright@latest \
    && playwright-cli --help \
    && playwright install --with-deps chromium \
    && rm -rf /var/lib/apt/lists/*
WORKDIR /app
ENV TZ=Asia/Shanghai
# é¢„ç¼–è¯‘å¥½çš„å¯¹åº”æž¶æž„äºŒè¿›åˆ¶ï¼ˆdist/amd64/cautiousmemory æˆ– dist/arm64/cautiousmemoryï¼‰
COPY dist/${TARGETARCH}/cautiousmemory /app/cautiousmemory
# å®ˆæŠ¤å¯åŠ¨è„šæœ¬ï¼šè¿›ç¨‹é€€å‡ºåŽæŒ‰é€€å‡ºç å†³å®šæ˜¯å¦é‡æ–°æ‹‰èµ·ï¼Œé¡µé¢ä¸€é”®æ›´æ–°é å®ƒå®Œæˆæ¢è£…ã€‚
# å®ƒåŒæ—¶è´Ÿè´£æŠŠ SIGTERM è½¬å‘ç»™ cautiousmemory â€”â€” docker stop åªæŠŠä¿¡å·å‘ç»™ PID 1ï¼Œ
# ä¸è½¬å‘çš„è¯ cautiousmemory æ”¶ä¸åˆ°ã€åšä¸äº†ä¼˜é›…å…³é—­ï¼Œ10 ç§’åŽè¢« SIGKILL ç¡¬æ€ã€‚
COPY start.sh /app/start.sh
RUN chmod +x /app/cautiousmemory /app/start.sh
COPY skills/ /app/skills/
# data/ï¼ˆSQLite + jwt.keyï¼‰æŒä¹…åŒ–ç‚¹
VOLUME ["/app/data"]
EXPOSE 8787 8788
ENTRYPOINT ["/app/start.sh"]
CMD ["-addr", ":8787", "-proxy", ":8788"]

