"use client";

// Shared components for LLM retry config: each of the five retry layers has its own "count + interval".
//
// Five layers from inner to outer: connect (SDK) → empty response (SDK) → same provider safety window → polling circuit breaker → intent rerun.
// The first three layers follow the endpoint, so each model config can override global defaults; the last two are process-level, global single copy.
//
// All inputs follow the same "blank = unconfigured" semantics, consistent with backend db.RetryRule:
//   attempts  blank/0 = built-in default | -1 = turn off this retry layer | >0 = use this count
//   interval  blank/0 = this layer's original exponential backoff | >0 = use this fixed ms interval instead

import * as React from "react";

import { Loader2Icon, SaveIcon } from "lucide-react";
import { toast } from "sonner";

import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { api } from "@/lib/api";
import type { LLMRetryOverride, LLMRetryPolicy, LLMRetryRule } from "@/lib/types";

export const ZERO_RULE: LLMRetryRule = { attempts: 0, interval_ms: 0 };
export const ZERO_OVERRIDE: LLMRetryOverride = {
  connect: ZERO_RULE,
  empty: ZERO_RULE,
  stream: ZERO_RULE,
};
const ZERO_POLICY: LLMRetryPolicy = {
  ...ZERO_OVERRIDE,
  breaker: ZERO_RULE,
  intent: ZERO_RULE,
};

type LayerMeta = {
  title: string;
  /** where this retry layer occurs, executed by whom */
  where: string;
  /** what errors fall into this layer—specific to status codes, don't make people guess */
  trigger: string;
  /** errors that look similar but DON'T fall into this layer—so people don't fill it and expect it to work */
  skips?: string;
  desc: string;
  attemptsLabel: string;
  /** default value when attempts is blank, used for placeholder */
  defAttempts: number;
  /** default interval strategy when interval is blank, used for placeholder */
  defInterval: string;
  /** meaning of attempts = -1 */
  offHint: string;
};

export const RETRY_LAYERS = {
  connect: {
    title: "Connect retry",
    where: "SDK · before getting 200",
    trigger:
      "Cannot connect or haven't received 200 yet: connection reset / read/write timeout / DNS failure etc. network layer errors, as well as HTTP 408, 429, 500, 502, 503, 504.",
    skips: "Other status codes (400 / 401 / 403 / 404 / 413 / 422 etc.) are deterministic rejections, retrying would also fail, directly throw.",
    desc: "Resend the same request as-is. Once the stream has started (already received 200), a mid-stream disconnect is not handled by this layer.",
    attemptsLabel: "Retry count",
    defAttempts: 3,
    defInterval: "0.5s→1s→2s exponential (capped at 8s)",
    offHint: "-1 = no retries at all, fail immediately on error",
  },
  empty: {
    title: "Empty response retry",
    where: "SDK · openai format only",
    trigger:
      "HTTP 200, finish_reason is normal stop, but the whole response has no content block at all—gateway empty frames, think field dropped frames, sampling hiccups all look like this.",
    skips: "No content due to max_tokens truncation doesn't count (that needs raising the output limit, retrying would just hit it again).",
    desc: "Resend the whole prompt, so it's more expensive on long contexts, don't set the count too high.",
    attemptsLabel: "Retry count",
    defAttempts: 2,
    defInterval: "0.5s→1s→2s exponential (capped at 8s)",
    offHint: "-1 = empty response passed through as-is",
  },
  stream: {
    title: "Same provider safety window retry",
    where: "This project · before output delivered",
    trigger:
      "After the stream is established (received 200) things go wrong: connection drops mid-stream, provider overloaded, 429 / 5xx error events within the stream—and not a single token has been handed to the caller yet.",
    skips:
      "Quota exhausted (402 / insufficient_quota, handled by polling for alternate config), context too long (413 / context length, handled by compression), 400 / 401 / 403 / 404 / 422 deterministic rejections, none retried.",
    desc: "Replay the same request on the same config. Since no output has been delivered yet, replaying won't duplicate model output or tool execution.",
    attemptsLabel: "Retry count",
    defAttempts: 2,
    defInterval: "0.5s→1s exponential (capped at 4s)",
    offHint: "-1 = stream break passed to outer intent rerun",
  },
  breaker: {
    title: "Polling circuit breaker",
    where: "This project · process-level, global single copy",
    trigger:
      "Circuit breaker trips when transient failures (429, 5xx, network errors) accumulate to threshold; deterministic failures like insufficient balance (402), invalid key (401 / 403), model not found (404) trip immediately without checking threshold.",
    skips: "One success clears to zero, so occasionally flaky configs won't gradually accumulate to trip the breaker.",
    desc: "After tripping, enters cooldown; during cooldown polling skips this config. State is persisted, survives restart.",
    attemptsLabel: "Consecutive failures to trip breaker",
    defAttempts: 3,
    defInterval: "1min→5min→30min gradient",
    offHint: "-1 = transient failures never trip breaker (deterministic failures still trip)",
  },
  intent: {
    title: "Intent rerun",
    where: "This project · process-level, global single copy",
    trigger:
      "None of the previous layers handled it: worker ends with model_error—inner retries all exhausted, or stream broke after already delivering output (replaying is unsafe then, must restart from scratch).",
    skips: "Quota exhaustion already handled by polling alternate config, not rerun here; tasks paused / terminated / entering wrapup yield immediately, not occupying backoff time.",
    desc: "Run the whole intent from the top again. It's the outermost layer; one rerun means the inner layers' counts all multiply again.",
    attemptsLabel: "Rerun count",
    defAttempts: 2,
    defInterval: "Fixed 3s",
    offHint: "-1 = no rerun, the intent is directly judged as blocked",
  },
} satisfies Record<string, LayerMeta>;

type LayerKey = keyof typeof RETRY_LAYERS;

/** Human-readable ms, only used for echo next to input fields so people don't count zeros. */
function humanMs(ms: number) {
  if (!Number.isFinite(ms) || ms <= 0) return "";
  if (ms < 1000) return `${ms}ms`;
  if (ms < 60_000) return `${Number((ms / 1000).toFixed(2))}s`;
  return `${Number((ms / 60_000).toFixed(2))}min`;
}

/** Controlled number input: empty string ↔ 0, intermediate states ("-", "1e") left as-is locally, not disturbing parent. */
function NumField({
  id,
  value,
  onChange,
  placeholder,
  min,
}: {
  id: string;
  value: number;
  onChange: (n: number) => void;
  placeholder: string;
  min: number;
}) {
  const [text, setText] = React.useState(value === 0 ? "" : String(value));
  // Parent replaced the entire value set (loaded policy, switched config) — keep in sync; local typing won't
  // reach here because then value already equals the parsed result of local text.
  React.useEffect(() => {
    const incoming = value === 0 ? "" : String(value);
    setText((cur) => (Number(cur || 0) === value ? cur : incoming));
  }, [value]);
  return (
    <Input
      id={id}
      type="number"
      min={min}
      className="w-28 shrink-0"
      value={text}
      placeholder={placeholder}
      onChange={(e) => {
        setText(e.target.value);
        const n = Number(e.target.value);
        onChange(e.target.value.trim() === "" || !Number.isFinite(n) ? 0 : Math.trunc(n));
      }}
    />
  );
}

/** Two knobs for one retry layer. idPrefix preserves label htmlFor when appearing multiple times on one page. */
export function RetryRuleFields({
  layer,
  idPrefix,
  value,
  onChange,
  compact,
}: {
  layer: LayerKey;
  idPrefix: string;
  value: LLMRetryRule;
  onChange: (r: LLMRetryRule) => void;
  /** true = compact version in config drawer: skips expanded explanations, only leaves "what errors fall into this layer" */
  compact?: boolean;
}) {
  const meta = RETRY_LAYERS[layer];
  const human = humanMs(value.interval_ms);
  return (
    <div className={compact ? "grid gap-2" : "grid gap-3 rounded-lg border p-3"}>
      <div className="grid gap-0.5">
        <div className="flex flex-wrap items-baseline gap-2">
          <Label className="text-sm">{meta.title}</Label>
          <span className="text-muted-foreground text-xs">{meta.where}</span>
        </div>
        {/* Which errors fall into this layer, specific to status codes—filled knobs but no effect? Probably the error never lands in this layer. */}
        <p className="text-muted-foreground text-xs">
          <span className="font-medium text-foreground">Triggered by</span>: {meta.trigger}
        </p>
        {!compact && meta.skips && (
          <p className="text-muted-foreground text-xs">
            <span className="font-medium text-foreground">Not handled here</span>: {meta.skips}
          </p>
        )}
        {!compact && <p className="text-muted-foreground text-xs">{meta.desc}</p>}
      </div>
      <div className="flex flex-wrap items-center gap-x-4 gap-y-2">
        <div className="flex items-center gap-2">
          <Label htmlFor={`${idPrefix}-${layer}-n`} className="text-muted-foreground text-xs">
            {meta.attemptsLabel}
          </Label>
          <NumField
            id={`${idPrefix}-${layer}-n`}
            min={-1}
            value={value.attempts}
            placeholder={`Default ${meta.defAttempts}`}
            onChange={(n) => onChange({ ...value, attempts: n })}
          />
        </div>
        <div className="flex items-center gap-2">
          <Label htmlFor={`${idPrefix}-${layer}-ms`} className="text-muted-foreground text-xs">
            Interval ms
          </Label>
          <NumField
            id={`${idPrefix}-${layer}-ms`}
            min={0}
            value={value.interval_ms}
            placeholder="Default backoff"
            onChange={(n) => onChange({ ...value, interval_ms: n })}
          />
          <span className="text-muted-foreground text-xs">{human ? `Fixed ${human}` : meta.defInterval}</span>
        </div>
      </div>
      {!compact && <p className="text-muted-foreground text-xs">Leave blank = use default; {meta.offHint}.</p>}
    </div>
  );
}

/** Three override layers in model config drawer (the three that follow the endpoint). */
export function ProfileRetryFields({
  value,
  onChange,
}: {
  value: LLMRetryOverride;
  onChange: (o: LLMRetryOverride) => void;
}) {
  return (
    <div className="grid gap-3 rounded-lg border p-3">
      <div className="grid gap-0.5">
        <Label className="text-sm">Retry overrides</Label>
        <p className="text-muted-foreground text-xs">
          Only takes effect for this config, overriding the global default in "Retry and backoff". Leave blank = follow global; attempts = -1 turns off this retry layer; interval set = use fixed interval instead of exponential backoff. Circuit breaker and intent rerun are process-level, only adjustable on the global page.
        </p>
      </div>
      {(["connect", "empty", "stream"] as const).map((k) => (
        <div key={k} className="border-t pt-3 first:border-t-0 first:pt-0">
          <RetryRuleFields
            compact
            layer={k}
            idPrefix="pf"
            value={value[k]}
            onChange={(r) => onChange({ ...value, [k]: r })}
          />
        </div>
      ))}
    </div>
  );
}

/** "Retry and backoff" tab: global default values for all five layers. */
export function RetryPolicyPanel() {
  const [policy, setPolicy] = React.useState<LLMRetryPolicy>(ZERO_POLICY);
  const [loading, setLoading] = React.useState(true);
  const [saving, setSaving] = React.useState(false);

  const load = React.useCallback(async () => {
    setLoading(true);
    try {
      const p = await api.llmRetryPolicy();
      setPolicy({ ...ZERO_POLICY, ...p });
    } catch (e) {
      toast.error(`Failed to read retry policy: ${(e as Error).message}`);
    } finally {
      setLoading(false);
    }
  }, []);

  React.useEffect(() => {
    void load();
  }, [load]);

  async function save() {
    if (saving) return;
    setSaving(true);
    try {
      // Backend clamps out-of-range values back into range and returns them; use returned values directly, what you see is what you get.
      const saved = await api.saveLLMRetryPolicy(policy);
      setPolicy({ ...ZERO_POLICY, ...saved });
      toast.success("Saved, takes effect immediately (current in-flight call still uses old params)");
    } catch (e) {
      toast.error(`Save failed: ${(e as Error).message}`);
    } finally {
      setSaving(false);
    }
  }

  const set = (k: LayerKey) => (r: LLMRetryRule) => setPolicy((p) => ({ ...p, [k]: r }));

  if (loading) {
    return (
      <div className="flex items-center gap-2 rounded-lg border border-dashed p-10 text-muted-foreground text-sm">
        <Loader2Icon className="size-4 animate-spin" /> Reading retry policy…
      </div>
    );
  }

  return (
    <div className="grid gap-4">
      <div className="rounded-lg border bg-muted/30 p-3 text-muted-foreground text-xs leading-relaxed">
        A model call failure goes through five retry layers in sequence, from inner to outer:
        <span className="text-foreground"> Connect → Empty response → Same provider safety window → Circuit breaker → Intent rerun</span>
        . Inner layers exhausted before outer layers, so counts are
        <span className="text-foreground"> multiplied</span>
        —max out every layer and one jitter could burn dozens of requests.
        Leave all blank for current default values, behavior identical to not having this page. The first three layers can be overridden per model config.
      </div>

      <div className="grid gap-3 md:grid-cols-2 xl:grid-cols-3">
        {(Object.keys(RETRY_LAYERS) as LayerKey[]).map((k) => (
          <RetryRuleFields key={k} layer={k} idPrefix="gl" value={policy[k]} onChange={set(k)} />
        ))}
      </div>

      <div className="flex gap-2">
        <Button onClick={save} disabled={saving}>
          {saving ? <Loader2Icon className="animate-spin" /> : <SaveIcon />}
          Save
        </Button>
        <Button variant="outline" onClick={() => setPolicy(ZERO_POLICY)} disabled={saving}>
          Restore all defaults
        </Button>
      </div>
    </div>
  );
}
