"use client";

import { Button } from "@/components/ui/button";

import type { RepeaterTab } from "./repeater-state";
import { useRepeaterStore } from "./repeater-store";

export function RepeaterHistory({ tab }: { tab?: RepeaterTab }) {
  const updateTab = useRepeaterStore((state) => state.updateTab);
  const busy = useRepeaterStore((state) => (tab ? state.inflight[tab.id] !== undefined : false));
  return (
    <section aria-label="Tab history" className="flex min-h-20 flex-1 flex-col gap-2 overflow-hidden border-t pt-3">
      <div className="flex items-center justify-between px-1 text-muted-foreground text-xs">
        <h3 className="font-medium">Tab history</h3>
        <span>{tab?.history.length ?? 0} entries</span>
      </div>
      <div className="flex min-h-0 flex-1 flex-col gap-1 overflow-y-auto">
        {!tab?.history.length ? (
          <p className="px-1 text-muted-foreground text-xs">Sent requests appear here</p>
        ) : (
          tab.history.map((entry) => (
            <Button
              key={entry.id}
              variant="ghost"
              disabled={busy}
              className="h-auto w-full shrink-0 flex-col items-start gap-1 px-2 py-2 text-left"
              onClick={() =>
                updateTab(tab.id, {
                  method: entry.method,
                  url: entry.url,
                  headersText: entry.headersText,
                  body: entry.body,
                  rawRequest: entry.rawRequest,
                  targetOrigin: entry.targetOrigin,
                  useProxy: entry.useProxy,
                  insecure: entry.insecure,
                  response: entry.response,
                })
              }
              title={entry.url}
            >
              <span className="flex w-full items-center justify-between gap-2 font-mono text-xs">
                <span>
                  {entry.method} {entry.response.status || "ERR"}
                </span>
                <time className="text-muted-foreground" dateTime={entry.at}>
                  {new Date(entry.at).toLocaleTimeString("en-US", { hour12: false })}
                </time>
              </span>
              <span className="w-full truncate text-muted-foreground text-xs">{entry.url}</span>
            </Button>
          ))
        )}
      </div>
    </section>
  );
}
