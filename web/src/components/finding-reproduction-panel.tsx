"use client";

import * as React from "react";

import { useRouter } from "next/navigation";
import { format } from "date-fns";

import { ChevronDownIcon, ChevronRightIcon, LoaderCircleIcon } from "lucide-react";
import { toast } from "sonner";
import { useTranslation } from "react-i18next";

import { HttpCodeBlock } from "@/components/http-code-block";
import { Markdown } from "@/components/markdown";
import { headersFromReqHead, requestFromEvidence } from "@/components/repeater/repeater-http";
import { seedFromEvidence } from "@/components/repeater/repeater-store";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { api } from "@/lib/api";
import type { Finding, FindingTrafficBinding, FindingTrafficDetail } from "@/lib/types";

type ReproductionStep = {
  number: number;
  title: string;
  body: string;
};

const REPRODUCTION_HEADINGS = ["Reproduction Steps", "Test Process", "Reproduction Process"];
const STEP_MARKER = /^\s*(?:###\s*)?(?:Step\s*)?(\d+)[.、:：)）]\s*(.+)$/gim;

const OVERVIEW_HEADINGS = ["Overview", "Impact and Risk", "Affected Scope"];
const OUTCOME_HEADING = "Final Outcome";
const ROOT_CAUSE_HEADING = "Root Cause Analysis";
const REMEDIATION_HEADING = "Remediation Advice";

function extractSection(report: string) {
  const match = report.match(new RegExp(`^##\\s+(?:${REPRODUCTION_HEADINGS.join("|")})\\s*$`, "im"));
  if (!match || match.index === undefined) return "";
  const start = match.index + match[0].length;
  const tail = report.slice(start);
  const end = tail.search(/^##\s+/m);
  return (end >= 0 ? tail.slice(0, end) : tail).trim();
}

function withoutPacket(text: string) {
  return text.replace(/```(?:http|bash|shell|sh|json|text)?\s*\n[\s\S]*?```/gi, "").trim();
}

function parseSteps(report: string, fallback: string): ReproductionStep[] {
  const section = extractSection(report);
  if (!section) {
    return [
      {
        number: 1,
        title: "Reproduction Process",
        body: fallback.trim() || "No structured reproduction steps; see the original report below.",
      },
    ];
  }

  const markers = [...section.matchAll(STEP_MARKER)];
  if (markers.length === 0) {
    return [{ number: 1, title: "Reproduction Process", body: withoutPacket(section) }];
  }

  return markers.map((marker, index) => {
    const start = marker.index ?? 0;
    const end = markers[index + 1]?.index ?? section.length;
    const raw = section.slice(start, end).trim();
    const firstLine = marker[2].trim();
    const rest = raw.slice(marker[0].length).trim();
    return {
      number: Number(marker[1]),
      title: firstLine,
      body: withoutPacket(rest),
    };
  });
}

function reportSection(report: string, headings: string[]) {
  const headingPattern = headings.join("|");
  const match = report.match(new RegExp(`^##\\s+(?:${headingPattern})\\s*$`, "im"));
  if (!match || match.index === undefined) return "";
  const tail = report.slice(match.index + match[0].length);
  const end = tail.search(/^##\s+/m);
  return (end >= 0 ? tail.slice(0, end) : tail).trim();
}

function packetText(detail: FindingTrafficDetail, side: "request" | "response") {
  const head = side === "request" ? detail.binding.snapshot.req_head : detail.binding.snapshot.resp_head;
  const body = detail[side].content;
  return [head, body].filter(Boolean).join("\n\n").trim();
}

function formatCapturedAt(value: number | string | null | undefined) {
  if (value === null || value === undefined || value === "") return "";
  const numeric = typeof value === "number" ? value : Number(value);
  const date = Number.isFinite(numeric)
    ? new Date(numeric < 10_000_000_000 ? numeric * 1000 : numeric)
    : new Date(String(value));
  if (Number.isNaN(date.getTime())) return "";
  return format(date, "MMM d, yyyy, h:mm a");
}

export function FindingReproductionPanel({ finding, contextTask }: { finding: Finding; contextTask?: string }) {
  const { t } = useTranslation();
  const steps = React.useMemo(
    () => parseSteps(finding.report ?? "", finding.evidence || finding.summary),
    [finding.evidence, finding.report, finding.summary],
  );
  const [traffic, setTraffic] = React.useState<FindingTrafficBinding[] | undefined>(undefined);
  const overviewSections = OVERVIEW_HEADINGS.map((heading) => ({
    heading,
    content: reportSection(finding.report ?? "", [heading]),
  })).filter((item) => item.content);
  const outcome = reportSection(finding.report ?? "", [OUTCOME_HEADING]);
  const rootCause = reportSection(finding.report ?? "", [ROOT_CAUSE_HEADING]);
  const remediation = reportSection(finding.report ?? "", [REMEDIATION_HEADING]);

  React.useEffect(() => {
    let active = true;
    api
      .findingTraffic(finding.id, contextTask)
      .then((value) => {
        if (active) setTraffic(value.bindings);
      })
      .catch(() => {
        if (active) setTraffic([]);
      });
    return () => {
      active = false;
    };
  }, [contextTask, finding.id]);

  const outcomeLabel = t("reproduction.final_outcome");
  const rootCauseLabel = t("reproduction.root_cause");
  const remediationLabel = t("reproduction.remediation");

  return (
    <Card>
      <CardHeader>
        <div className="flex flex-wrap items-center justify-between gap-2">
          <div>
            <CardTitle>{t("reproduction.reproduction_steps")}</CardTitle>
            <CardDescription>{t("reproduction.card_desc")}</CardDescription>
          </div>
          <Badge variant={steps.length > 0 ? "secondary" : "outline"}>
            {t("reproduction.steps_count", { steps: steps.length, packets: traffic?.length ?? finding.traffic_count ?? 0 })}
          </Badge>
        </div>
      </CardHeader>
      <CardContent className="space-y-3">
        {overviewSections.map((section) => (
          <div key={section.heading} className="rounded-lg bg-muted/30 px-3 py-2.5 text-sm">
            <Markdown text={`## ${section.heading}\n\n${section.content}`} />
          </div>
        ))}
        {steps.map((step, index) => {
          const binding = traffic?.find((item) => item.position === index) ?? traffic?.[index];
          return (
            <div key={`${step.number}-${step.title}`} className="relative rounded-lg border bg-background p-4">
              <div className="min-w-0">
                <div className="flex flex-wrap items-center justify-between gap-2">
                  <div className="flex min-w-0 items-center gap-2">
                    <Badge variant="outline" className="shrink-0 font-medium">
                      {t("reproduction.step", { number: step.number })}
                    </Badge>
                    <h3 className="min-w-0 break-words font-semibold text-sm leading-5">{step.title}</h3>
                  </div>
                </div>
                {step.body ? <Markdown text={step.body} /> : null}
                <PacketDisclosure findingId={finding.id} bindingId={binding?.id} contextTask={contextTask} />
              </div>
            </div>
          );
        })}
        {outcome || rootCause || remediation ? (
          <div className="space-y-3 border-t pt-3">
            {outcome ? <Markdown text={`## ${t("reproduction.final_outcome")}\n\n${outcome}`} /> : null}
            {rootCause ? <Markdown text={`## ${t("reproduction.root_cause")}\n\n${rootCause}`} /> : null}
            {remediation ? <Markdown text={`## ${t("reproduction.remediation")}\n\n${remediation}`} /> : null}
          </div>
        ) : null}
      </CardContent>
    </Card>
  );
}

function PacketDisclosure({
  findingId,
  bindingId,
  contextTask,
}: {
  findingId: string;
  bindingId?: string;
  contextTask?: string;
}) {
  const { t } = useTranslation();
  const [open, setOpen] = React.useState(false);
  const [detail, setDetail] = React.useState<FindingTrafficDetail | null>(null);
  const [error, setError] = React.useState("");
  const [repeaterBusy, setRepeaterBusy] = React.useState(false);
  const router = useRouter();

  React.useEffect(() => {
    if (!open || !bindingId || detail) return;
    let active = true;
    setError("");
    api
      .findingTrafficDetail(findingId, bindingId, contextTask)
      .then((value) => {
        if (active) setDetail(value);
      })
      .catch((e: Error) => {
        if (active) setError(e.message);
      });
    return () => {
      active = false;
    };
  }, [bindingId, contextTask, detail, findingId, open]);

  if (!bindingId) {
    return <p className="mt-3 border-t pt-3 text-xs text-muted-foreground">{t("reproduction.no_bound_evidence")}</p>;
  }

  async function sendToRepeater() {
    if (!detail) return;
    setRepeaterBusy(true);
    try {
      if (detail.request.binary) toast.info(t("reproduction.request_binary"));
      else if (detail.request.truncated) toast.info(t("reproduction.request_preview"));
      seedFromEvidence(
        requestFromEvidence(
          {
            method: detail.binding.snapshot.method || "GET",
            url: detail.binding.snapshot.url || "",
            headersText: headersFromReqHead(detail.binding.snapshot.req_head),
            body: detail.request.binary ? "" : detail.request.content || "",
          },
          detail.binding.snapshot.req_head,
        ),
        { findingId, bindingId: detail.binding.id },
      );
      router.push("/function/repeater");
    } catch (e) {
      toast.error(t("reproduction.send_repeater_failed", { error: (e as Error).message }));
    } finally {
      setRepeaterBusy(false);
    }
  }

  return (
    <div className="mt-3 border-t pt-3">
      <div className="flex flex-wrap gap-2">
        <Button
          type="button"
          variant="outline"
          size="sm"
          className="h-7 gap-1.5 text-xs"
          aria-expanded={open}
          onClick={() => setOpen((current) => !current)}
        >
          <ChevronDownIcon />
          {open ? t("reproduction.collapse_packet") : t("reproduction.view_packet")}
        </Button>
        {open && detail ? (
          <Button
            type="button"
            variant="outline"
            size="sm"
            className="h-7 text-xs"
            disabled={repeaterBusy}
            onClick={() => void sendToRepeater()}
          >
            {repeaterBusy ? t("reproduction.preparing") : t("reproduction.send_to_repeater")}
          </Button>
        ) : null}
      </div>
      {open ? (
        <div className="mt-3 rounded-lg border bg-muted/20 p-3">
          {error ? <p className="text-destructive text-xs">{t("reproduction.packet_load_failed", { error })}</p> : null}
          {!error && bindingId && !detail ? (
            <div className="flex items-center gap-2 text-muted-foreground text-xs">
              <LoaderCircleIcon className="size-3.5 animate-spin" /> {t("reproduction.reading_traffic")}
            </div>
          ) : null}
          {detail ? (
            <div className="space-y-3">
              <div className="flex min-w-0 items-center gap-2 text-xs">
                <Badge variant="secondary">{detail.binding.snapshot.method}</Badge>
                <Badge variant="outline">{detail.binding.snapshot.status}</Badge>
                <span className="min-w-0 flex-1 break-all text-muted-foreground">{detail.binding.snapshot.url}</span>
                <span className="shrink-0 whitespace-nowrap text-muted-foreground">
                  {formatCapturedAt(detail.binding.snapshot.captured_at) || t("reproduction.time_unknown")}
                </span>
              </div>
              <div>
                <p className="mb-1 text-[11px] text-muted-foreground">Request</p>
                <HttpCodeBlock raw={packetText(detail, "request")} />
              </div>
              <div>
                <p className="mb-1 text-[11px] text-muted-foreground">Response</p>
                <HttpCodeBlock raw={packetText(detail, "response")} />
              </div>
            </div>
          ) : null}
        </div>
      ) : null}
    </div>
  );
}
