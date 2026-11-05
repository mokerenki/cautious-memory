"use client";

import * as React from "react";

import {
  CheckIcon,
  ChevronDown,
  ChevronRight,
  CrosshairIcon,
  Flag,
  MessageSquare,
  PaperclipIcon,
  ShieldAlertIcon,
  Terminal,
  UserIcon,
  Wrench,
  XIcon,
} from "lucide-react";
import { useTranslation } from "react-i18next";
import { toast } from "sonner";

import { ApprovalDetail } from "@/components/approval-records";
import { Markdown } from "@/components/markdown";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Collapsible, CollapsibleContent, CollapsibleTrigger } from "@/components/ui/collapsible";
import { api } from "@/lib/api";
import type { Activity, InterceptPending } from "@/lib/types";

// ---- per-agent lane color (planner + work#1/#2/#3 …) ---------------------------
const workerColors = ["bg-sky-600", "bg-violet-600", "bg-teal-600", "bg-pink-600", "bg-orange-600"];
function workerColor(name: string): string {
  if (name === "planner") return "bg-amber-600";
  if (name === "mainagent") return "bg-primary";
  const m = /#(\d+)/.exec(name);
  const i = m ? (parseInt(m[1], 10) - 1) % workerColors.length : 0;
  return workerColors[Math.max(0, i)];
}

const chip = (worker: string) =>
  "mt-0.5 shrink-0 rounded px-1 text-[9px] font-medium text-white " + workerColor(worker);

// useInView latches true when the ref'd element first comes within `rootMargin` of
// the enclosing scroll viewport. Blocks that always show their full body (user
// bubbles, answers) use it to defer fetching that body until they're about to be
// seen — so opening a long thread doesn't fire a detail request for every off-
// screen step. Observes the ScrollArea viewport (falls back to eager load when
// IntersectionObserver is unavailable, e.g. SSR).
function useInView(rootMargin = "400px"): [React.RefObject<HTMLDivElement | null>, boolean] {
  const ref = React.useRef<HTMLDivElement | null>(null);
  const [inView, setInView] = React.useState(false);
  React.useEffect(() => {
    if (inView) return;
    const el = ref.current;
    if (!el) return;
    if (typeof IntersectionObserver === "undefined") {
      setInView(true);
      return;
    }
    const root = el.closest('[data-slot="scroll-area-viewport"]') as HTMLElement | null;
    const ob = new IntersectionObserver(
      (entries) => {
        if (entries.some((e) => e.isIntersecting)) setInView(true);
      },
      { root, rootMargin },
    );
    ob.observe(el);
    return () => ob.disconnect();
  }, [inView, rootMargin]);
  return [ref, inView];
}

// A tool group pairs a tool_use with its matching tool_result (by tool_use_id);
// a run of consecutive conversational steps (text/thinking/result) from the same
// agent is one "message".
type Group =
  | { type: "user"; key: number; step: Activity; intent?: boolean }
  | { type: "answer"; key: number; step: Activity }
  | { type: "round"; key: number; label: string }
  | { type: "tool"; key: number; worker: string; use?: Activity; result?: Activity }
  | { type: "msg"; key: number; worker: string; steps: Activity[] }
  | { type: "intercept"; key: number; step: Activity };

function groupSteps(steps: Activity[], chat: boolean): Group[] {
  const out: Group[] = [];
  const byToolId = new Map<string, Extract<Group, { type: "tool" }>>();
  for (const s of steps) {
    if (s.kind === "usage") continue;
    if (s.kind === "round") {
      out.push({ type: "round", key: s.seq, label: s.summary || "New Round" });
      continue;
    }
    if (s.kind === "intercept_request") {
      out.push({ type: "intercept", key: s.seq, step: s });
      continue;
    }
    if (s.kind === "user" || s.kind === "intent") {
      out.push({ type: "user", key: s.seq, step: s, intent: s.kind === "intent" });
      continue;
    }
    if (s.kind === "result" || (chat && s.kind === "text")) {
      out.push({ type: "answer", key: s.seq, step: s });
      continue;
    }
    if (s.kind === "tool_use") {
      const g: Extract<Group, { type: "tool" }> = { type: "tool", key: s.seq, worker: s.worker, use: s };
      if (s.tool_use_id) byToolId.set(s.tool_use_id, g);
      out.push(g);
      continue;
    }
    if (s.kind === "tool_result") {
      const g = s.tool_use_id ? byToolId.get(s.tool_use_id) : undefined;
      if (g && !g.result) g.result = s;
      else out.push({ type: "tool", key: s.seq, worker: s.worker, result: s });
      continue;
    }
    const last = out[out.length - 1];
    if (last && last.type === "msg" && last.worker === s.worker) last.steps.push(s);
    else out.push({ type: "msg", key: s.seq, worker: s.worker, steps: [s] });
  }
  return out;
}

const kindLabel = (k: string) => (k === "thinking" ? "Reasoning" : k === "result" ? "Summary" : "Note");

function ActivityTime({ ts }: { ts: string }) {
  const date = new Date(ts);
  if (!ts || Number.isNaN(date.getTime())) return null;
  return (
    <time
      dateTime={date.toISOString()}
      title={date.toLocaleString("en-US")}
      className="text-[10px] text-muted-foreground tabular-nums"
      suppressHydrationWarning
    >
      {date.toLocaleString("en-US", {
        month: "2-digit",
        day: "2-digit",
        hour: "2-digit",
        minute: "2-digit",
        hour12: false,
      })}
    </time>
  );
}

function toolInputText(tool: string, raw: string): string {
  if (tool !== "Bash") return raw;
  try {
    const o = JSON.parse(raw);
    if (o && typeof o.command === "string") return o.command;
  } catch {}
  const m = raw.match(/"command"\s*:\s*"((?:\\.|[^"\\])*)/);
  if (m) {
    try {
      return JSON.parse('"' + m[1] + '"');
    } catch {
      return m[1].replace(/\\(["\\/nrt])/g, (_s, c) => (c === "n" ? "\n" : c === "r" ? "\r" : c === "t" ? "\t" : c));
    }
  }
  return raw;
}

function InterceptCard({ step, getDetail }: { step: Activity; getDetail: (seq: number) => Promise<string> }) {
  const { t } = useTranslation();
  const pendingId = React.useMemo(() => {
    const m = /\(#(\d+)\)/.exec(step.summary);
    return m ? parseInt(m[1], 10) : null;
  }, [step.summary]);

  const toolName = React.useMemo(() => {
    const m = /Tool\s+(\S+)\s+request/.exec(step.summary);
    return m ? m[1] : step.summary;
  }, [step.summary]);

  const [detail, setDetail] = React.useState<Record<string, unknown> | null>(null);
  const [decided, setDecided] = React.useState<"allowed" | "denied" | "timeout" | null>(null);
  const [deciding, setDeciding] = React.useState(false);
  const [expanded, setExpanded] = React.useState(false);
  const [pending, setPending] = React.useState<InterceptPending | null>(null);
  const [statusError, setStatusError] = React.useState("");
  const [retry, setRetry] = React.useState(0);

  React.useEffect(() => {
    let live = true;
    getDetail(step.seq)
      .then((raw) => {
        if (!live || !raw) return;
        try {
          setDetail(JSON.parse(raw));
        } catch {
          /* ignore */
        }
      })
      .catch(() => {
        /* ignore */
      });
    return () => {
      live = false;
    };
  }, [step.seq, getDetail]);

  React.useEffect(() => {
    if (!pendingId) return;
    let live = true;
    api
      .interceptGetOne(pendingId)
      .then((p) => {
        if (!live) return;
        setPending(p);
        setStatusError("");
        if (p.status !== "pending") setDecided(p.status as "allowed" | "denied" | "timeout");
      })
      .catch((error) => {
        if (live) setStatusError((error as Error).message || t("transcript.detail_load_failed"));
      });
    return () => {
      live = false;
    };
  }, [pendingId, retry]);

  async function decide(decision: "allowed" | "denied") {
    if (step.inherited || !pendingId || deciding) return;
    setDeciding(true);
    try {
      await api.interceptDecide(pendingId, decision);
      setDecided(decision);
      toast.success(decision === "allowed" ? t("transcript.allowed") : t("transcript.denied"));
    } catch (e) {
      toast.error((e as Error).message);
    } finally {
      setDeciding(false);
    }
  }

  const inputStr = detail?.input ? JSON.stringify(detail.input).slice(0, 200) : null;

  const row = pending
    ? { ...pending, status: decided || pending.status, conv_title: "", conv_agent_key: "", rule_name: "" }
    : null;

  return (
    <div className="my-2 rounded-lg border border-amber-400/50 bg-amber-50/40 dark:bg-amber-950/15 p-3 text-xs">
      <div className="flex flex-wrap items-start justify-between gap-3">
        <div className="flex items-start gap-2 min-w-0">
          <ShieldAlertIcon className="mt-0.5 h-3.5 w-3.5 shrink-0 text-amber-500" />
          <div className="min-w-0 space-y-0.5">
            <div className="flex items-center gap-1.5 font-medium">
              <span className="text-amber-700 dark:text-amber-400">{t("transcript.approval_request")}</span>
              <code className="rounded bg-amber-100 dark:bg-amber-900/50 px-1 font-mono text-amber-800 dark:text-amber-300">
                {toolName}
              </code>
              {pendingId && <span className="text-muted-foreground">#{pendingId}</span>}
            </div>
            {inputStr && <p className="font-mono text-muted-foreground truncate">{inputStr}</p>}
          </div>
        </div>

        {step.inherited ? (
          <Badge variant="outline">{t("transcript.history_readonly")}</Badge>
        ) : decided ? (
          <span
            className={
              "shrink-0 rounded px-2 py-0.5 text-[11px] font-medium " +
              (decided === "allowed"
                ? "bg-emerald-100 text-emerald-700 dark:bg-emerald-900/40 dark:text-emerald-400"
                : decided === "timeout"
                  ? "bg-zinc-100 text-zinc-600 dark:bg-zinc-800 dark:text-zinc-400"
                  : "bg-red-100 text-red-700 dark:bg-red-900/40 dark:text-red-400")
            }
          >
            {decided === "allowed"
              ? t("transcript.allowed")
              : decided === "timeout"
                ? t("transcript.timeout")
                : t("transcript.denied")}
          </span>
        ) : (
          <div className="flex shrink-0 gap-1.5">
            <Button
              size="sm"
              className="h-6 px-2 text-xs"
              disabled={deciding || !pendingId}
              onClick={() => decide("allowed")}
            >
              <CheckIcon className="h-3 w-3" />
              {t("transcript.allow")}
            </Button>
            <Button
              size="sm"
              variant="destructive"
              className="h-6 px-2 text-xs"
              disabled={deciding || !pendingId}
              onClick={() => decide("denied")}
            >
              <XIcon className="h-3 w-3" />
              {t("transcript.deny")}
            </Button>
          </div>
        )}
      </div>
      {pendingId ? (
        <Collapsible open={expanded} onOpenChange={setExpanded} className="mt-2 min-w-0">
          <CollapsibleTrigger asChild>
            <Button
              variant="ghost"
              size="sm"
              aria-label={expanded ? t("transcript.collapse_detail") : t("transcript.expand_detail")}
            >
              {expanded ? <ChevronDown data-icon="inline-start" /> : <ChevronRight data-icon="inline-start" />}
              {expanded ? t("transcript.collapse_detail") : t("transcript.expand_detail")}
            </Button>
          </CollapsibleTrigger>
          <CollapsibleContent>
            {expanded && row ? (
              <ApprovalDetail
                row={row}
                busy={deciding}
                decide={(_id, decision) => decide(decision)}
                revision={retry}
                readOnly={Boolean(step.inherited)}
                defaultExpanded
                onResolved={setDecided}
              />
            ) : statusError ? (
              <div className="flex flex-wrap items-center gap-2 p-3" role="alert">
                <span>{statusError}</span>
                <Button variant="outline" size="sm" onClick={() => setRetry((value) => value + 1)}>
                  {t("transcript.retry_detail")}
                </Button>
              </div>
            ) : (
              <p className="p-3 text-muted-foreground">{t("transcript.loading_detail")}</p>
            )}
          </CollapsibleContent>
        </Collapsible>
      ) : null}
    </div>
  );
}

function ToolBlock({
  group,
  getDetail,
  showWorker,
  focused = false,
}: {
  group: Extract<Group, { type: "tool" }>;
  getDetail: (seq: number) => Promise<string>;
  showWorker?: boolean;
  focused?: boolean;
}) {
  const { t } = useTranslation();
  const [open, setOpen] = React.useState(focused);
  const targetRef = React.useRef<HTMLElement>(null);
  const [detail, setDetail] = React.useState<string | null>(null);
  const loadedKey = React.useRef<string | null>(null);
  const { use, result } = group;
  const toolName = use?.tool || result?.tool || "Tool";
  const ToolIcon = toolName === "Bash" ? Terminal : Wrench;
  const running = !result;
  const ok = !!result && !result.is_error;
  const statusTone = running
    ? "text-muted-foreground"
    : ok
      ? "text-emerald-600 dark:text-emerald-400"
      : "text-red-600 dark:text-red-400";
  const rawCmd =
    use && use.summary.startsWith(toolName) ? use.summary.slice(toolName.length).trimStart() : (use?.summary ?? "");
  const cmd = toolInputText(toolName, rawCmd);
  const statusText = running ? t("transcript.executing") : ok ? "✓" : "✕ " + t("transcript.failed");

  const detailKey = `${use?.seq ?? ""}:${result?.seq ?? ""}`;
  React.useEffect(() => {
    if (!open || loadedKey.current === detailKey) return;
    let live = true;
    const segs: { label: string; seq: number }[] = [];
    if (use) segs.push({ label: t("transcript.command"), seq: use.seq });
    if (result) segs.push({ label: t("transcript.output") + (result.is_error ? " ✕" : " ✓"), seq: result.seq });
    void Promise.all(
      segs.map((x) =>
        getDetail(x.seq)
          .then((d) => d || t("transcript.empty"))
          .catch(() => t("transcript.load_failed")),
      ),
    ).then((parts) => {
      if (!live) return;
      setDetail(
        segs
          .map(
            (x, i) =>
              `[[${x.label}]]\n${x.label === t("transcript.command") ? toolInputText(toolName, parts[i]) : parts[i]}`,
          )
          .join("\n\n"),
      );
      loadedKey.current = detailKey;
    });
    return () => {
      live = false;
    };
  }, [open, detailKey, use, result, getDetail, toolName]);

  const scrolledRef = React.useRef(false);
  React.useEffect(() => {
    scrolledRef.current = false;
    if (focused) setOpen(true);
  }, [focused]);
  React.useEffect(() => {
    if (!focused || !open || detail === null || scrolledRef.current) return;
    const el = targetRef.current;
    const viewport = el?.closest('[data-slot="scroll-area-viewport"]');
    if (!el || !viewport) return;
    scrolledRef.current = true;
    const center = () => {
      const rect = el.getBoundingClientRect();
      const bounds = viewport.getBoundingClientRect();
      viewport.scrollTop += rect.top + rect.height / 2 - bounds.top - bounds.height / 2;
    };
    const observer = new ResizeObserver(center);
    observer.observe(el.parentElement ?? el);
    observer.observe(viewport);
    const stop = () => observer.disconnect();
    viewport.addEventListener("wheel", stop, { passive: true, once: true });
    viewport.addEventListener("touchstart", stop, { passive: true, once: true });
    viewport.addEventListener("pointerdown", stop, { once: true });
    const frame = requestAnimationFrame(center);
    const timer = setTimeout(stop, 2000);
    return () => {
      cancelAnimationFrame(frame);
      clearTimeout(timer);
      stop();
      viewport.removeEventListener("wheel", stop);
      viewport.removeEventListener("touchstart", stop);
      viewport.removeEventListener("pointerdown", stop);
    };
  }, [focused, open, detail]);

  function toggle() {
    setOpen((o) => !o);
  }

  return (
    <section
      ref={targetRef}
      aria-label={focused ? `Focused tool call #${use?.seq}` : undefined}
      className={focused ? "rounded-lg border-2 border-primary bg-primary/5 p-3 text-xs" : "text-xs"}
    >
      <button type="button" onClick={toggle} className="flex w-full items-start gap-2 py-1 text-left hover:bg-muted/40">
        <span className="mt-0.5 text-muted-foreground">
          {open ? <ChevronDown className="size-3" /> : <ChevronRight className="size-3" />}
        </span>
        <ToolIcon className={"mt-0.5 size-3.5 shrink-0 " + (running ? "text-sky-600 dark:text-sky-400" : statusTone)} />
        {showWorker && <span className={chip(group.worker)}>{group.worker}</span>}
        <span className="shrink-0 font-medium text-sky-600 dark:text-sky-400">{toolName}</span>
        {cmd && <span className="min-w-0 flex-1 truncate font-mono text-muted-foreground">{cmd}</span>}
        <span className={"ml-auto shrink-0 font-medium " + statusTone}>{statusText}</span>
      </button>
      {open && (
        <pre className="ml-7 mb-1 max-h-72 overflow-auto whitespace-pre-wrap break-all rounded bg-muted/50 p-2 font-mono text-[11px] leading-relaxed">
          {detail ?? t("transcript.loading")}
        </pre>
      )}
    </section>
  );
}

function MessageBlock({
  group,
  getDetail,
  showWorker,
}: {
  group: Extract<Group, { type: "msg" }>;
  getDetail: (seq: number) => Promise<string>;
  showWorker?: boolean;
}) {
  const { t } = useTranslation();
  const [open, setOpen] = React.useState(false);
  const [detail, setDetail] = React.useState<string | null>(null);
  const loadedKey = React.useRef<string | null>(null);
  const speak = group.steps.filter((s) => s.kind !== "thinking");
  const hasThinking = group.steps.some((s) => s.kind === "thinking");
  const isError = group.steps.some((s) => s.is_error);
  const body = (speak.length ? speak : group.steps).map((s) => s.summary).join("  ") || "…";
  const Icon = isError ? Flag : MessageSquare;
  const tone = isError ? "text-red-600 dark:text-red-400" : "text-foreground";

  const detailKey = group.steps.map((s) => s.seq).join(",");
  React.useEffect(() => {
    if (!open || loadedKey.current === detailKey) return;
    let live = true;
    void Promise.all(
      group.steps.map((s) =>
        getDetail(s.seq)
          .then((d) => d || s.summary)
          .catch(() => s.summary),
      ),
    ).then((parts) => {
      if (!live) return;
      setDetail(group.steps.map((s, i) => `[[${kindLabel(s.kind)}]]\n${parts[i]}`).join("\n\n"));
      loadedKey.current = detailKey;
    });
    return () => {
      live = false;
    };
  }, [open, detailKey, group.steps, getDetail]);

  function toggle() {
    setOpen((o) => !o);
  }

  return (
    <div className="text-xs">
      <button type="button" onClick={toggle} className="flex w-full items-start gap-2 py-1 text-left hover:bg-muted/40">
        <span className="mt-0.5 text-muted-foreground">
          {open ? <ChevronDown className="size-3" /> : <ChevronRight className="size-3" />}
        </span>
        <Icon className={"mt-0.5 size-3.5 shrink-0 " + tone} />
        {showWorker && <span className={chip(group.worker)}>{group.worker}</span>}
        <span className={"min-w-0 flex-1 truncate " + tone}>
          {body}
          {hasThinking && (
            <span className="ml-1 text-[10px] text-muted-foreground">· {t("transcript.contains_thinking")}</span>
          )}
        </span>
      </button>
      {open && (
        <pre className="ml-7 mb-1 max-h-72 overflow-auto whitespace-pre-wrap break-all rounded bg-muted/50 p-2 font-mono text-[11px] leading-relaxed">
          {detail ?? t("transcript.loading")}
        </pre>
      )}
    </div>
  );
}

function fmtBytes(n: number): string {
  if (n >= 1 << 20) return `${(n / (1 << 20)).toFixed(1)} MB`;
  if (n >= 1 << 10) return `${(n / (1 << 10)).toFixed(1)} KB`;
  return `${n} B`;
}

type MsgAttachment = { name: string; path: string; size: number };

function parseUserBody(body: string): { text: string; attachments: MsgAttachment[] } {
  if (body.startsWith("{")) {
    try {
      const p = JSON.parse(body);
      if (p && Array.isArray(p.attachments)) {
        return { text: typeof p.text === "string" ? p.text : "", attachments: p.attachments };
      }
    } catch {}
  }
  return { text: body, attachments: [] };
}

function UserRow({
  step,
  intent,
  getDetail,
}: {
  step: Activity;
  intent?: boolean;
  getDetail: (seq: number) => Promise<string>;
}) {
  const Icon = intent ? CrosshairIcon : UserIcon;
  const [ref, inView] = useInView();
  const inline = step.detail && step.detail.length > 0 ? step.detail : null;
  const [full, setFull] = React.useState<string | null>(inline);
  React.useEffect(() => {
    if (inline) return;
    if (!inView) return;
    let live = true;
    getDetail(step.seq)
      .then((d) => {
        if (live) setFull(d || step.summary);
      })
      .catch(() => {
        if (live) setFull(step.summary);
      });
    return () => {
      live = false;
    };
  }, [inView, step.seq, getDetail, step.summary, inline]);
  const { text, attachments } = parseUserBody(full ?? step.summary);
  return (
    <div ref={ref} className="mt-3 mb-2 flex min-w-0 justify-end gap-2">
      <div className="flex min-w-0 max-w-[85%] flex-col items-end gap-1.5">
        {attachments.length > 0 && (
          <div className="flex min-w-0 max-w-full flex-wrap justify-end gap-1.5">
            {attachments.map((a) => (
              <div
                key={a.path}
                className="flex min-w-0 max-w-full items-center gap-1.5 rounded-md border bg-card px-2 py-1 text-xs shadow-sm"
                title={a.path}
              >
                <PaperclipIcon className="size-3 shrink-0 text-primary" />
                <span className="max-w-[180px] truncate font-medium">{a.name}</span>
                <span className="shrink-0 text-muted-foreground">{fmtBytes(a.size)}</span>
              </div>
            ))}
          </div>
        )}
        {text && (
          <div className="min-w-0 max-w-full whitespace-pre-wrap rounded-lg rounded-tr-sm bg-primary px-3 py-1.5 text-sm text-primary-foreground [overflow-wrap:anywhere]">
            {text}
          </div>
        )}
        <ActivityTime ts={step.ts} />
      </div>
      <div className="mt-0.5 flex size-6 shrink-0 items-center justify-center rounded-full bg-primary/10">
        <Icon className="size-3.5 text-primary" />
      </div>
    </div>
  );
}

function AnswerBlock({ step, getDetail }: { step: Activity; getDetail: (seq: number) => Promise<string> }) {
  const [ref, inView] = useInView();
  const [full, setFull] = React.useState<string | null>(null);
  React.useEffect(() => {
    if (!inView) return;
    let live = true;
    getDetail(step.seq)
      .then((d) => {
        if (live) setFull(d || step.summary);
      })
      .catch(() => {
        if (live) setFull(step.summary);
      });
    return () => {
      live = false;
    };
  }, [inView, step.seq, getDetail, step.summary]);
  return (
    <div ref={ref} className="mb-2 mt-1 flex min-w-0 flex-col gap-1">
      <div
        className={
          "min-w-0 flex-1 break-words rounded-lg bg-muted px-3 py-2 " +
          (step.is_error ? "text-sm text-red-600 dark:text-red-400" : "")
        }
      >
        {step.is_error ? (
          <span className="whitespace-pre-wrap">{full ?? step.summary}</span>
        ) : (
          <Markdown text={full ?? step.summary} />
        )}
      </div>
      <ActivityTime ts={step.ts} />
    </div>
  );
}

function ExecView({
  activity,
  taskId,
  chat,
  fetchDetail,
  focusedSeq,
}: {
  activity: Activity[];
  taskId?: string;
  chat?: boolean;
  fetchDetail?: (seq: number) => Promise<string>;
  focusedSeq?: number;
}) {
  const showWorker = new Set(activity.map((a) => a.worker)).size > 1;
  const getDetail = React.useCallback(
    (seq: number) => (fetchDetail ? fetchDetail(seq) : api.activityDetail(seq, taskId).then((r) => r.detail ?? "")),
    [fetchDetail, taskId],
  );
  return (
    <div className="flex flex-col">
      {groupSteps(activity, !!chat).map((g) =>
        g.type === "round" ? (
          <div key={"r" + g.key} className="my-2 flex items-center gap-2 text-[10px] font-medium text-muted-foreground">
            <span className="h-px flex-1 bg-border" />
            {g.label}
            <span className="h-px flex-1 bg-border" />
          </div>
        ) : g.type === "user" ? (
          <UserRow key={"u" + g.key} step={g.step} intent={g.intent} getDetail={getDetail} />
        ) : g.type === "answer" ? (
          <AnswerBlock key={"a" + g.key} step={g.step} getDetail={getDetail} />
        ) : g.type === "tool" ? (
          <ToolBlock
            key={"t" + g.key}
            group={g}
            getDetail={getDetail}
            showWorker={showWorker}
            focused={focusedSeq != null && g.use?.seq === focusedSeq}
          />
        ) : g.type === "intercept" ? (
          <InterceptCard key={"ic" + g.key} step={g.step} getDetail={getDetail} />
        ) : (
          <MessageBlock key={"m" + g.key} group={g} getDetail={getDetail} showWorker={showWorker} />
        ),
      )}
    </div>
  );
}

export function Transcript({
  activity,
  live,
  taskId,
  chat,
  fetchDetail,
  focusedSeq,
}: {
  activity: Activity[];
  live?: boolean;
  taskId?: string;
  chat?: boolean;
  fetchDetail?: (seq: number) => Promise<string>;
  focusedSeq?: number;
}) {
  const { t } = useTranslation();
  const transcriptRef = React.useRef<HTMLDivElement>(null);
  const [focusPadding, setFocusPadding] = React.useState(0);
  React.useLayoutEffect(() => {
    if (focusedSeq == null) {
      setFocusPadding(0);
      return;
    }
    const viewport = transcriptRef.current?.closest('[data-slot="scroll-area-viewport"]');
    if (!viewport) return;
    const measure = () => setFocusPadding(viewport.clientHeight / 2);
    measure();
    const observer = new ResizeObserver(measure);
    observer.observe(viewport);
    return () => observer.disconnect();
  }, [focusedSeq]);
  return (
    <div
      ref={transcriptRef}
      className="flex flex-col gap-1"
      style={focusPadding ? { paddingBlock: focusPadding } : undefined}
    >
      <ExecView activity={activity} taskId={taskId} chat={chat} fetchDetail={fetchDetail} focusedSeq={focusedSeq} />
      {live && (
        <div className="flex items-center gap-2 pl-2 pt-1 text-xs text-muted-foreground">
          <span className="flex gap-1">
            <span className="size-1.5 animate-bounce rounded-full bg-blue-500 [animation-delay:-0.3s]" />
            <span className="size-1.5 animate-bounce rounded-full bg-blue-500 [animation-delay:-0.15s]" />
            <span className="size-1.5 animate-bounce rounded-full bg-blue-500" />
          </span>
          {t("transcript.streaming")}
        </div>
      )}
    </div>
  );
}
