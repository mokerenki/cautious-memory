"use client";

import * as React from "react";

import {
  CheckCircle2Icon,
  DownloadIcon,
  ExternalLinkIcon,
  RefreshCwIcon,
  RotateCcwIcon,
  TriangleAlertIcon,
} from "lucide-react";
import { toast } from "sonner";

import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { Progress } from "@/components/ui/progress";
import { api, sseUrl } from "@/lib/api";
import type { UpdateCheck, UpdateProgress } from "@/lib/types";

/** 等待新版本上线的最长时间。一次升级要经过三次进程启动（暂存 → 换装 → 新版），
 *  每次都是秒级，三分钟足够覆盖慢磁盘和 Docker 容器重建。 */
const RESTART_TIMEOUT_MS = 180_000;

function humanSize(n?: number): string {
  if (!n || n <= 0) return "";
  const units = ["B", "KB", "MB", "GB"];
  let v = n;
  let i = 0;
  while (v >= 1024 && i < units.length - 1) {
    v /= 1024;
    i++;
  }
  return `${v.toFixed(i === 0 ? 0 : 1)} ${units[i]}`;
}

const sleep = (ms: number) => new Promise((r) => setTimeout(r, ms));

export function UpdateCard() {
  const [info, setInfo] = React.useState<UpdateCheck | null>(null);
  const [checking, setChecking] = React.useState(true);
  const [progress, setProgress] = React.useState<UpdateProgress | null>(null);
  // 与 progress 分开：暂存完成后进程就没了，SSE 会断，此时要切到轮询 /api/health。
  const [restarting, setRestarting] = React.useState(false);
  const [busy, setBusy] = React.useState(false);

  // quiet 同时决定要不要绕过后端缓存：进页面时的自动检查用缓存（顶栏刚查过），
  // 用户手动点「检查更新」则强制回源，否则刚发布的版本要等缓存过期才看得到。
  const check = React.useCallback((quiet = false) => {
    setChecking(true);
    api
      .checkUpdate(!quiet)
      .then((r) => {
        setInfo(r);
        if (!quiet) {
          if (r.error) toast.error("Update check failed: " + r.error);
          else if (r.has_update) toast.success(`New version available ${r.latest}`);
          else if (r.comparable) toast.success("Already on latest version");
        }
      })
      .catch((e) => {
        if (!quiet) toast.error("Update check failed: " + (e as Error).message);
      })
      .finally(() => setChecking(false));
  }, []);

  React.useEffect(() => {
    check(true);
  }, [check]);

  // 轮询 /api/health 直到版本号变化。
  //
  // 判据必须是"版本变了"而不是"能连上了"：换装过程中旧版本会短暂地重新起来一次
  // （那一次只负责把 artex.new 换上去然后立刻退出），只看连通性会误判成功。
  const waitForNewVersion = React.useCallback(async (fromVersion: string) => {
    setRestarting(true);
    const deadline = Date.now() + RESTART_TIMEOUT_MS;
    while (Date.now() < deadline) {
      await sleep(2000);
      try {
        const r = await fetch("/api/health", { cache: "no-store" });
        if (r.ok) {
          const j = (await r.json()) as { version?: string };
          if (j.version && j.version !== fromVersion) {
            toast.success(`Updated to ${j.version}, reloading page`);
            await sleep(800);
            window.location.reload();
            return;
          }
        }
      } catch {
        // 重启窗口内连不上是预期的，继续轮询。
      }
    }
    setRestarting(false);
    toast.error("Service restart timeout. Check backend logs, or confirm artex is started via start.sh / start.bat.");
  }, []);

  // 订阅更新进度。SSE 不走 Next 的 /api 重写（那层会缓冲，事件推不出来）。
  const openStream = React.useCallback(
    (fromVersion: string) => {
      const es = new EventSource(sseUrl("/api/update/stream"));
      es.onmessage = (ev) => {
        let p: UpdateProgress;
        try {
          p = JSON.parse(ev.data) as UpdateProgress;
        } catch {
          return;
        }
        setProgress(p);
        if (p.phase === "failed") {
          es.close();
          setBusy(false);
          toast.error("Update failed: " + (p.error || p.message));
          return;
        }
        if (p.phase === "staged") {
          es.close();
          void waitForNewVersion(fromVersion);
        }
      };
      es.onerror = () => {
        // 进程退出时 SSE 必然断开。如果已经进入等待重启，这属于正常现象，
        // 交给 /api/health 轮询继续判定即可。
        es.close();
      };
      return es;
    },
    [waitForNewVersion],
  );

  const doUpdate = () => {
    if (!info) return;
    const from = info.current;
    const ok = window.confirm(
      `Confirm update to ${info.latest}?\n\n` +
        "Update will restart the program, running tasks will be interrupted.\n" +
        (info.mode === "docker"
          ? "\nNote: container update only replaces the program itself, will not update playwright / nmap etc. toolchain in the image; " +
            "if the new version depends on new tools, use docker compose pull instead."
          : ""),
    );
    if (!ok) return;

    setBusy(true);
    setProgress({ phase: "downloading", percent: 0, message: "Preparing…" });
    const es = openStream(from);
    api.applyUpdate().catch((e) => {
      es.close();
      setBusy(false);
      setProgress(null);
      toast.error("Update start failed: " + (e as Error).message);
    });
  };

  const doRollback = () => {
    if (!info) return;
    if (
      !window.confirm(
        "Rollback to previous version?\n\nProgram will restart, running tasks will be interrupted.\nNote: database structure will not roll back, old version may not recognize data written by new version.",
      )
    )
      return;
    const from = info.current;
    setBusy(true);
    api
      .rollbackUpdate()
      .then(() => {
        toast.success("Switched to previous version, restarting…");
        void waitForNewVersion(from);
      })
      .catch((e) => {
        setBusy(false);
        toast.error("Rollback failed: " + (e as Error).message);
      });
  };

  const phase = progress?.phase;
  const showProgress = busy || restarting;
  // 只有下载阶段拿得到真实百分比（按 Content-Length 算）。校验/解压/等待重启都是
  // 时长不可知的阶段，进度条填满并加个脉冲动画表示"在忙但说不准还要多久"。
  const downloading = !restarting && phase === "downloading";
  const pct = downloading ? Math.max(progress?.percent ?? 0, 0) : 100;

  return (
    // 设置页是多列瀑布流布局，卡片自己负责行间距并禁止跨列断开（见 page.tsx 的注释）。
    <Card className="mb-4 break-inside-avoid md:mb-6">
      <CardHeader>
        <CardTitle className="flex items-center gap-2 text-base">
          <DownloadIcon className="size-4" />
          Version & Updates
        </CardTitle>
        <CardDescription>Check and install new versions from GitHub. Update will restart the program, running tasks will be interrupted.</CardDescription>
      </CardHeader>
      <CardContent className="space-y-4">
        <div className="flex flex-wrap items-center gap-2 text-sm">
          <span className="text-muted-foreground">Current version</span>
          <Badge variant="secondary" className="font-mono">
            {info?.current ?? "…"}
          </Badge>
          {info && (
            <>
              <Badge variant="outline" className="font-mono">
                {info.os}/{info.arch}
              </Badge>
              <Badge variant="outline">                {info.mode === "docker" ? "Docker" : "Standalone"}</Badge>
            </>
          )}
          {info?.latest && (
            <>
              <span className="text-muted-foreground">Latest version</span>
              <Badge variant={info.has_update ? "default" : "secondary"} className="font-mono">
                {info.latest}
              </Badge>
            </>
          )}
          {info?.html_url && (
            <a
              href={info.html_url}
              target="_blank"
              rel="noreferrer"
              className="inline-flex items-center gap-1 text-xs text-muted-foreground underline-offset-4 hover:underline"
            >
              Release notes <ExternalLinkIcon className="size-3" />
            </a>
          )}
        </div>

        {info?.boot_notice && (
          <p className="flex items-start gap-2 rounded-md border border-amber-500/40 bg-amber-500/10 p-2 text-xs text-amber-700 dark:text-amber-400">
            <TriangleAlertIcon className="mt-0.5 size-3.5 shrink-0" />
            {info.boot_notice}
          </p>
        )}

        {info?.error && (
          <p className="flex items-start gap-2 rounded-md border border-destructive/40 bg-destructive/10 p-2 text-xs text-destructive">
            <TriangleAlertIcon className="mt-0.5 size-3.5 shrink-0" />
            Cannot connect to GitHub: {info.error}
            {" "}You may configure global proxy above and retry.
          </p>
        )}

        {info && !info.comparable && info.reason && <p className="text-xs text-muted-foreground">{info.reason}</p>}

        {info?.has_update && info.asset_available === false && (
          <p className="flex items-start gap-2 rounded-md border border-destructive/40 bg-destructive/10 p-2 text-xs text-destructive">
            <TriangleAlertIcon className="mt-0.5 size-3.5 shrink-0" />
            {info.latest} no release package for {info.os}/{info.arch} (missing {info.asset}), cannot auto-update.
          </p>
        )}

        {info?.has_update && info.asset_available !== false && (
          <p className="text-xs text-muted-foreground">
            Will download <span className="font-mono">{info.asset}</span>
            {info.size ? ` (${humanSize(info.size)})` : ""}, verify SHA256 and smoke test before replacing, failure keeps current version automatically.
          </p>
        )}

        {info && !info.has_update && info.comparable && !info.error && (
          <p className="flex items-center gap-2 text-xs text-muted-foreground">
            <CheckCircle2Icon className="size-3.5 text-emerald-600" />
            Already on latest version.
          </p>
        )}

        {info?.mode === "docker" && info.has_update && (
          <p className="text-xs text-muted-foreground">
            Docker update only replaces the program itself, does not update playwright / nmap etc. toolchain in the image, and
            <span className="font-mono"> docker compose up -d </span>
            rebuilding container will revert to image's built-in version. To upgrade image together, run
            <span className="font-mono"> docker compose pull artex &amp;&amp; docker compose up -d artex</span>.
          </p>
        )}

        {showProgress && (
          <div className="space-y-1.5">
            <Progress value={pct} className={downloading ? undefined : "animate-pulse"} />
            <p className="text-xs text-muted-foreground">
              {restarting ? "Restarting and applying new version, please wait (page will auto refresh)…" : progress?.message}
            </p>
          </div>
        )}

        <div className="flex flex-wrap gap-2">
          <Button variant="outline" size="sm" onClick={() => check(false)} disabled={checking || busy || restarting}>
            <RefreshCwIcon className={checking ? "size-4 animate-spin" : "size-4"} />
            Check updates
          </Button>
          <Button
            size="sm"
            onClick={doUpdate}
            disabled={busy || restarting || !info?.has_update || info?.asset_available === false}
          >
            <DownloadIcon className="size-4" />
            {info?.has_update ? `Update to ${info.latest}` : "Update now"}
          </Button>
          {info?.has_backup && (
            <Button variant="ghost" size="sm" onClick={doRollback} disabled={busy || restarting}>
              <RotateCcwIcon className="size-4" />
              Rollback to previous version
            </Button>
          )}
        </div>

          <p className="text-xs text-muted-foreground">
            One-click update relies on daemon script to restart the program. Please start ARTEX via <span className="font-mono">start.sh</span> (Windows:
            <span className="font-mono"> start.bat</span>); running artex binary directly, the program will not be auto-restarted after exit.
          </p>
      </CardContent>
    </Card>
  );
}
