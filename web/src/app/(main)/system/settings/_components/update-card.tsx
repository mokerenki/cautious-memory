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
import { Field, FieldDescription, FieldGroup, FieldLabel } from "@/components/ui/field";
import { Input } from "@/components/ui/input";
import { Progress } from "@/components/ui/progress";
import { api, openSSE } from "@/lib/api";
import type { UpdateCheck, UpdateProgress, UpdateSettings } from "@/lib/types";

/** Maximum time to wait for a new version to come online. One upgrade goes through three process startups (staging → swap → new version), each seconds-level; three minutes is enough to cover slow disks and Docker container rebuilds. */
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
  // Separate from progress: once staging is complete, the process is gone and SSE disconnects. At that point, switch to polling /api/version.
  const [restarting, setRestarting] = React.useState(false);
  const [busy, setBusy] = React.useState(false);

  const [network, setNetwork] = React.useState<UpdateSettings | null>(null);
  const [savedNetwork, setSavedNetwork] = React.useState<UpdateSettings | null>(null);
  const [savingNetwork, setSavingNetwork] = React.useState(false);
  const [networkError, setNetworkError] = React.useState("");
  const streamRef = React.useRef<ReturnType<typeof openSSE> | null>(null);
  const networkDirty = network?.proxy !== savedNetwork?.proxy;

  React.useEffect(() => {
    let active = true;
    api
      .getUpdateSettings()
      .then((settings) => {
        if (!active) return;
        setNetwork(settings);
        setSavedNetwork(settings);
      })
      .catch((e) => {
        if (active) setNetworkError("Failed to read update network settings: " + (e as Error).message);
      });
    return () => {
      active = false;
    };
  }, []);

  React.useEffect(() => () => streamRef.current?.close(), []);

  // quiet also determines whether to bypass the backend cache: the automatic check on page load uses the cache (the top bar just checked),
  // while clicking “Check for updates” manually forces a request to the origin; otherwise, a newly released version won’t appear until the cache expires.
  const check = React.useCallback((quiet = false) => {
    setChecking(true);
    api
      .checkUpdate(!quiet)
      .then((r) => {
        setInfo(r);
        if (!quiet) {
          if (r.error) toast.error("Failed to check for updates: " + r.error);
          else if (r.has_update) toast.success(`Found new version ${r.latest}`);
          else if (r.comparable) toast.success("Already on the latest version");
        }
      })
      .catch((e) => {
        if (!quiet) toast.error("Failed to check for updates: " + (e as Error).message);
      })
      .finally(() => setChecking(false));
  }, []);

  React.useEffect(() => {
    check(true);
  }, [check]);

  const saveNetwork = async () => {
    if (!network) return;
    setSavingNetwork(true);
    setNetworkError("");
    try {
      const settings = await api.saveUpdateSettings({
        proxy: network.proxy,
      });
      setNetwork(settings);
      setSavedNetwork(settings);
      toast.success("Update network settings saved");
      check();
    } catch (e) {
      setNetworkError((e as Error).message);
    } finally {
      setSavingNetwork(false);
    }
  };

  // Poll /api/version with the admin JWT until the version number changes.
  //
  // The criterion must be “the version changed,” not “the server is reachable”: during replacement, the old version briefly starts up again
  // (that run only replaces artex.new and then exits immediately), so checking connectivity alone would give a false positive.
  const waitForNewVersion = React.useCallback(async (fromVersion: string) => {
    setRestarting(true);
    const deadline = Date.now() + RESTART_TIMEOUT_MS;
    while (Date.now() < deadline) {
      await sleep(2000);
      try {
        const { version } = await api.version();
        if (version && version !== fromVersion) {
          toast.success(`Updated to ${version}, reloading page`);
          await sleep(800);
          window.location.reload();
          return;
        }
      } catch {
        // The server being unreachable during the restart window is expected; keep polling.
      }
    }
    setRestarting(false);
    setBusy(false);
    toast.error("Waiting for service restart timed out. Please check backend logs, or confirm artex is started via start.sh / start.bat.");
  }, []);

  // Subscribe to update progress. SSE doesn’t go through Next’s /api rewrite (that layer buffers responses and prevents events from being sent).
  const openStream = React.useCallback(
    (fromVersion: string) => {
      streamRef.current?.close();
      const es = openSSE("/api/update/stream");
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
      streamRef.current = es;
      es.onerror = () => {
        // SSE will inevitably disconnect when the process exits. If we’re already waiting for a restart, that’s expected;
        // let polling /api/version continue to determine when it’s done.
        // EventSource reconnects automatically and resumes the current progress saved by the server.
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
        "Updating will restart the program, running tasks will be interrupted.\n" +
        (info.mode === "docker"
          ? "\nNote: container updates only replace the program itself, will not update playwright / nmap etc. toolchains inside the image; " +
            "if the new version depends on new tools, please use docker compose pull instead."
          : ""),
    );
    if (!ok) return;

    setBusy(true);
    setProgress({ phase: "downloading", percent: 0, message: "Preparing…" });
    api
      .applyUpdate()
      .then(() => {
        openStream(from);
      })
      .catch((e) => {
        setBusy(false);
        setProgress(null);
        toast.error("Failed to start update: " + (e as Error).message);
      });
  };

  const doRollback = () => {
    if (!info) return;
    if (
      !window.confirm(
        "Roll back to previous version?\n\nThe program will restart, running tasks will be interrupted.\nNote: database structure will not roll back, old versions may not recognize data written by the new version.",
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
  // Only the download stage provides a real percentage (calculated from Content-Length). Verification, extraction, and waiting for restart are
  // stages of unknown duration, so fill the progress bar and add a pulse animation to indicate “busy, but the remaining time is unknown.”
  const downloading = !restarting && phase === "downloading";
  const pct = downloading ? Math.max(progress?.percent ?? 0, 0) : 100;

  return (
    // The settings page uses a multi-column masonry layout. Cards handle their own vertical spacing and are prevented from breaking across columns (see the comment in page.tsx).
    <Card className="mb-4 break-inside-avoid md:mb-6">
      <CardHeader>
        <CardTitle className="flex items-center gap-2 text-base">
          <DownloadIcon className="size-4" />
          Version & updates
        </CardTitle>
        <CardDescription>Check and install new versions from GitHub. Updating will restart the program, running tasks will be interrupted.</CardDescription>
      </CardHeader>
      <CardContent className="flex flex-col gap-4">
        <FieldGroup>
          <Field data-invalid={!!networkError} data-disabled={busy || restarting || savingNetwork || !network}>
            <FieldLabel htmlFor="update-proxy" className="text-sm font-normal text-muted-foreground">
              Update proxy (optional)
            </FieldLabel>
            <div className="flex items-center gap-2">
              <Input
                id="update-proxy"
                autoComplete="off"
                placeholder="http://host:port or socks5://host:port (blank = direct connect)"
                value={network?.proxy ?? ""}
                disabled={busy || restarting || savingNetwork || !network}
                aria-invalid={!!networkError}
                onChange={(e) =>
                  network &&
                  setNetwork({
                    proxy: e.target.value,
                  })
                }
              />
              <Button
                type="button"
                onClick={() => void saveNetwork()}
                disabled={!network || savingNetwork || checking || busy || restarting}
              >
                {savingNetwork ? "Saving…" : "Save"}
              </Button>
            </div>
            <FieldDescription className="text-xs">
              Independent egress proxy, only used for checking versions and downloading updates, supports HTTP / HTTPS / SOCKS5. Direct connect after saving blank; does not affect tasks, web search, or LLM proxy.
            </FieldDescription>
            {networkDirty && <p className="text-xs text-muted-foreground">Changes not yet saved, will automatically check for updates after saving.</p>}
            {networkError && (
              <p role="alert" className="text-xs text-destructive">
                {networkError}
              </p>
            )}
          </Field>
        </FieldGroup>
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
              <Badge variant="outline">{info.mode === "docker" ? "Docker" : "Standalone"}</Badge>
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
              Changelog <ExternalLinkIcon className="size-3" />
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
            Unable to connect to GitHub: {info.error}
            {" "}Configure update proxy or switch to direct connect and retry.
          </p>
        )}

        {info && !info.comparable && info.reason && <p className="text-xs text-muted-foreground">{info.reason}</p>}

        {info?.has_update && info.asset_available === false && (
          <p className="flex items-start gap-2 rounded-md border border-destructive/40 bg-destructive/10 p-2 text-xs text-destructive">
            <TriangleAlertIcon className="mt-0.5 size-3.5 shrink-0" />
            {info.latest} does not provide a release package for {info.os}/{info.arch} (missing {info.asset}), cannot auto-update.
          </p>
        )}

        {info?.has_update && info.asset_available !== false && (
            <p className="text-xs text-muted-foreground">
              Will download <span className="font-mono">{info.asset}</span>
              {info.size ? ` (${humanSize(info.size)})` : ""}, will replace after SHA256 verification and smoke test, failure automatically keeps current version.
          </p>
        )}

        {info && !info.has_update && info.comparable && !info.error && (
          <p className="flex items-center gap-2 text-xs text-muted-foreground">
            <CheckCircle2Icon className="size-3.5 text-emerald-600" />
            Already on the latest version.
          </p>
        )}

        {info?.mode === "docker" && info.has_update && (
            <p className="text-xs text-muted-foreground">
              Docker updates only replace the program itself, do not update playwright / nmap etc. toolchains in the image, and
              <span className="font-mono"> docker compose up -d </span>
              after container rebuild reverts to image's bundled version. To upgrade the image together, run
              <span className="font-mono"> docker compose pull artex &amp;&amp; docker compose up -d artex</span>。
          </p>
        )}

        {progress?.phase === "failed" && (
          <p role="alert" className="text-xs text-destructive">
            Update failed: {progress.error || progress.message}
          </p>
        )}

        {showProgress && (
          <div className="space-y-1.5">
            <Progress value={pct} className={downloading ? undefined : "animate-pulse"} />
            <p className="text-xs text-muted-foreground">
              {restarting ? "Restarting and applying new version, please wait (page will auto-refresh)…" : progress?.message}
            </p>
          </div>
        )}

        <div className="flex flex-wrap gap-2">
          <Button
            variant="outline"
            size="sm"
            onClick={() => check(false)}
            disabled={checking || busy || restarting || networkDirty || savingNetwork || !network}
          >
            <RefreshCwIcon className={checking ? "size-4 animate-spin" : "size-4"} />
            Check for updates
          </Button>
          <Button
            size="sm"
            onClick={doUpdate}
            disabled={
              checking ||
              networkDirty ||
              savingNetwork ||
              !network ||
              busy ||
              restarting ||
              !!info?.error ||
              !info?.has_update ||
              info?.asset_available === false
            }
          >
            <DownloadIcon className="size-4" />
            {info?.has_update ? `Update to ${info.latest}` : "Update now"}
          </Button>
          {info?.has_backup && (
            <Button variant="ghost" size="sm" onClick={doRollback} disabled={busy || restarting}>
              <RotateCcwIcon className="size-4" />
              Roll back to previous version
            </Button>
          )}
        </div>

          <p className="text-xs text-muted-foreground">
            One-click update relies on the guardian script to restart the program. Please start ARTEX via <span className="font-mono">start.sh</span> (Windows: <span className="font-mono">start.bat</span>); running artex directly, the program won't be automatically restarted after exit.
        </p>
      </CardContent>
    </Card>
  );
}
