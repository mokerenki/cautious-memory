"use client";

import * as React from "react";

import { CpuIcon, FlaskConicalIcon, KeyboardIcon, RadioTowerIcon, SearchIcon, ShieldAlertIcon } from "lucide-react";
import { toast } from "sonner";

import { Button } from "@/components/ui/button";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";
import { Switch } from "@/components/ui/switch";
import { api } from "@/lib/api";
import { CHAT_SEND_MODE_OPTIONS, type ChatSendMode, setChatSendMode, useChatSendMode } from "@/lib/chat-send-mode";
import type { Settings } from "@/lib/types";

import { UpdateCard } from "./_components/update-card";

export default function SystemSettingsPage() {
  const [trafficCapture, setTrafficCapture] = React.useState(false);
  const [agentTrafficBinding, setAgentTrafficBinding] = React.useState(false);
  const [webSearch, setWebSearch] = React.useState(false);
  const [backend, setBackend] = React.useState("ddgs");
  const [braveKeySet, setBraveKeySet] = React.useState(false);
  const [braveKeyInput, setBraveKeyInput] = React.useState("");
  const [tavilyKeySet, setTavilyKeySet] = React.useState(false);
  const [tavilyKeyInput, setTavilyKeyInput] = React.useState("");
  const [savingTavilyKey, setSavingTavilyKey] = React.useState(false);
  const [proxyInput, setProxyInput] = React.useState("");
  const [savingProxy, setSavingProxy] = React.useState(false);
  const [globalProxyInput, setGlobalProxyInput] = React.useState("");
  const [savingGlobalProxy, setSavingGlobalProxy] = React.useState(false);
  const [testing, setTesting] = React.useState(false);
  const [loaded, setLoaded] = React.useState(false);
  const [saving, setSaving] = React.useState(false);
  const [savingKey, setSavingKey] = React.useState(false);
  const [pyInterp, setPyInterp] = React.useState("");
  const [workers, setWorkers] = React.useState("3");
  const [savingWorkers, setSavingWorkers] = React.useState(false);
  // 操作约束注入范围(默认都开)。
  const [injectPlanner, setInjectPlanner] = React.useState(true);
  const [injectWorker, setInjectWorker] = React.useState(true);
  // 实验功能:noa 上下文压缩(默认关)。
  const [noaCompaction, setNoaCompaction] = React.useState(false);
  // 纯前端偏好：不走 /api/settings，直接读写 localStorage。
  const sendMode = useChatSendMode();

  const apply = React.useCallback((s: Settings) => {
    setTrafficCapture(!!s.traffic_capture);
    setAgentTrafficBinding(!!s.agent_traffic_binding);
    setWebSearch(!!s.web_search_enabled);
    setBackend(s.web_search_backend || "ddgs");
    setBraveKeySet(!!s.brave_key_set);
    setTavilyKeySet(!!s.tavily_key_set);
    setProxyInput(s.web_search_proxy ?? "");
    setGlobalProxyInput(s.global_proxy ?? "");
    setPyInterp(s.python_interpreter ?? "");
    setWorkers(String(s.workers ?? 3));
    setInjectPlanner(s.constraints_inject_planner !== false);
    setInjectWorker(s.constraints_inject_worker !== false);
    setNoaCompaction(!!s.noa_compaction);
  }, []);

  const saveWorkers = () => {
    const n = Number(workers);
    if (!Number.isInteger(n) || n <= 0) {
      toast.error("Concurrency must be an integer greater than 0");
      return;
    }
    setSavingWorkers(true);
    api
      .setSettings({ workers: n })
      .then((s) => {
        apply(s);
        toast.success("Saved concurrent work agent count (takes effect for subsequently started tasks)");
      })
      .catch((e) => toast.error("Save failed: " + (e as Error).message))
      .finally(() => setSavingWorkers(false));
  };

  const savePython = () => {
    setSaving(true);
    api
      .setSettings({ python_interpreter: pyInterp.trim() })
      .then((s) => {
        apply(s);
        toast.success("Saved Python interpreter configuration");
      })
      .catch((e) => toast.error("Save failed: " + (e as Error).message))
      .finally(() => setSaving(false));
  };
  const detectPython = () => {
    setSaving(true);
    api
      .detectPython()
      .then((r) => setPyInterp(r.python_interpreter))
      .catch(() => undefined)
      .finally(() => setSaving(false));
  };

  React.useEffect(() => {
    api
      .settings()
      .then(apply)
      .catch(() => undefined)
      .finally(() => setLoaded(true));
  }, [apply]);

  const toggleTraffic = (v: boolean) => {
    setTrafficCapture(v); // optimistic
    setSaving(true);
    api
      .setSettings({ traffic_capture: v })
      .then(apply)
      .catch(() => setTrafficCapture(!v)) // revert on failure
      .finally(() => setSaving(false));
  };

  const toggleInjectPlanner = (v: boolean) => {
    setInjectPlanner(v); // optimistic
    api
      .setSettings({ constraints_inject_planner: v })
      .then(apply)
      .catch(() => setInjectPlanner(!v)); // revert on failure
  };

  const toggleAgentTrafficBinding = (v: boolean) => {
    setAgentTrafficBinding(v);
    setSaving(true);
    api
      .setSettings({ agent_traffic_binding: v })
      .then((s) => {
        apply(s);
        toast.success(v ? "Agent auto traffic binding enabled" : "Agent auto traffic binding disabled");
      })
      .catch((e) => {
        setAgentTrafficBinding(!v);
        toast.error(`Save failed: ${(e as Error).message}`);
      })
      .finally(() => setSaving(false));
  };

  const toggleInjectWorker = (v: boolean) => {
    setInjectWorker(v); // optimistic
    api
      .setSettings({ constraints_inject_worker: v })
      .then(apply)
      .catch(() => setInjectWorker(!v)); // revert on failure
  };

  const toggleNoaCompaction = (v: boolean) => {
    setNoaCompaction(v); // optimistic
    api
      .setSettings({ noa_compaction: v })
      .then((s) => {
        apply(s);
        toast.success(v ? "noa context compression enabled (takes effect for subsequently started runs)" : "noa context compression disabled (restore built-in compression)");
      })
      .catch((e) => {
        setNoaCompaction(!v); // revert on failure
        toast.error(`Save failed: ${(e as Error).message}`);
      });
  };

  // Persist a web-search patch (enable and/or backend). Optimistic with refetch.
  const saveWebSearch = (patch: Partial<Settings>) => {
    setSaving(true);
    api
      .setSettings(patch)
      .then((s) => {
        apply(s);
        toast.success("Saved web search configuration");
      })
      .catch((e) => {
        toast.error("保存失败：" + (e as Error).message);
        api
          .settings()
          .then(apply)
          .catch(() => undefined);
      })
      .finally(() => setSaving(false));
  };

  const saveBraveKey = () => {
    setSavingKey(true);
    api
      .setSettings({ brave_search_api_key: braveKeyInput })
      .then((s) => {
        apply(s);
        setBraveKeyInput("");
        toast.success("Saved Brave API Key");
      })
      .catch((e) => toast.error("Save failed: " + (e as Error).message))
      .finally(() => setSavingKey(false));
  };

  const saveTavilyKey = () => {
    setSavingTavilyKey(true);
    api
      .setSettings({ tavily_search_api_key: tavilyKeyInput })
      .then((s) => {
        apply(s);
        setTavilyKeyInput("");
        toast.success("Saved Tavily API Key");
      })
      .catch((e) => toast.error("Save failed: " + (e as Error).message))
      .finally(() => setSavingTavilyKey(false));
  };

  const saveProxy = () => {
    setSavingProxy(true);
    api
      .setSettings({ web_search_proxy: proxyInput.trim() })
      .then((s) => {
        apply(s);
        toast.success(proxyInput.trim() ? "Saved exit proxy" : "Cleared exit proxy (direct connection)");
      })
      .catch((e) => toast.error("Save failed: " + (e as Error).message))
      .finally(() => setSavingProxy(false));
  };

  const saveGlobalProxy = () => {
    setSavingGlobalProxy(true);
    api
      .setSettings({ global_proxy: globalProxyInput.trim() })
      .then((s) => {
        apply(s);
        toast.success(globalProxyInput.trim() ? "Saved global proxy" : "Cleared global proxy (direct connection)");
      })
      .catch((e) => toast.error("Save failed: " + (e as Error).message))
      .finally(() => setSavingGlobalProxy(false));
  };

  // Run a real "test" search ("test") against the CURRENT form values (backend +
  // proxy + entered key), falling back to saved values server-side. Toasts result.
  const runTest = () => {
    setTesting(true);
    api
      .testWebSearch({
        web_search_backend: backend,
        web_search_proxy: proxyInput.trim(),
        brave_search_api_key: braveKeyInput,
        tavily_search_api_key: tavilyKeyInput,
      })
      .then((r) => {
        if (r.ok) toast.success(`Search test successful · ${r.backend} returned ${r.count} results`);
        else toast.error("Search test failed: " + (r.error || "Unknown error"));
      })
      .catch((e) => toast.error("Search test failed: " + (e as Error).message))
      .finally(() => setTesting(false));
  };

  // brave-free selected but no key stored and none being entered → tool stays off.
  const braveNeedsKey = webSearch && backend === "brave-free" && !braveKeySet;

  return (
    <div className="flex flex-1 flex-col gap-4 md:gap-6">
      <div>
        <h1 className="text-xl font-semibold tracking-tight">System Configuration</h1>
        <p className="text-muted-foreground text-sm">Global runtime toggles</p>
      </div>

      {/* 多列而非 grid：网络搜索卡片比其余高数倍，且高度随所选后端变化（brave/tavily
          的 key 输入是条件渲染）。grid 会按最高的一张撑满整行、在旁边留下大片空白，
          多列则自动按内容高度平衡填充。卡片间距靠 mb 而非 gap——多列布局下
          column-gap 只管列间距，行间距要由子元素自己给。 */}
      <div className="columns-1 gap-4 md:gap-6 lg:columns-2">
        <UpdateCard />

        <Card className="mb-4 break-inside-avoid md:mb-6">
          <CardHeader>
              <CardTitle className="flex items-center gap-2 text-base">
                <RadioTowerIcon className="size-4" />
                Traffic Capture
              </CardTitle>
              <CardDescription>
                After enabling, all Agent HTTP traffic is recorded in full via the logging proxy, and the traffic_search / traffic_get
                tools plus proxy configuration are injected into the Agent (prompt includes proxy instructions).
                <br />
                Off (default) means no traffic is recorded: the Agent
                <b>will not</b> receive proxy configuration or traffic tools, and the prompt
                <b>does not contain</b> proxy-related content. Switching takes effect immediately by rebuilding Agents.
              </CardDescription>
          </CardHeader>
          <CardContent className="flex items-center justify-between gap-4">
              <Label htmlFor="traffic-capture" className="text-sm font-normal text-muted-foreground">
                {trafficCapture ? "Enabled · Recording traffic and injecting proxy" : "Disabled · No traffic recording, no proxy injection"}
              </Label>
            <Switch
              id="traffic-capture"
              checked={trafficCapture}
              disabled={!loaded || saving}
              onCheckedChange={toggleTraffic}
            />
          </CardContent>
        </Card>

        <Card className="mb-4 break-inside-avoid md:mb-6">
          <CardHeader>
              <CardTitle className="flex items-center gap-2 text-base">
                <RadioTowerIcon className="size-4" />
                Agent Auto Traffic Binding
              </CardTitle>
              <CardDescription id="agent-traffic-binding-description">
                Default off. When enabled, the report Agent triggered when a finding is ingested will check existing HTTP requests/responses,
                associate corresponding traffic before writing the report.
                <b>Inspecting packets and extra tool calls will increase token consumption.</b>
                <br />
                Reporting still works normally for TCP, uncaptured, or unmatched traffic. This toggle does not affect traffic capture, manual binding, or viewing saved evidence.
                Takes effect for the next Agent round; turning off immediately rejects new automatic binding.
              </CardDescription>
          </CardHeader>
          <CardContent className="flex items-center justify-between gap-4">
              <Label htmlFor="agent-traffic-binding" className="text-sm font-normal text-muted-foreground">
                {agentTrafficBinding ? "Enabled · Increases token consumption" : "Disabled · Manual binding still available"}
              </Label>
            <Switch
              id="agent-traffic-binding"
              aria-describedby="agent-traffic-binding-description"
              checked={agentTrafficBinding}
              disabled={!loaded || saving}
              onCheckedChange={toggleAgentTrafficBinding}
            />
          </CardContent>
        </Card>

        <Card className="mb-4 break-inside-avoid md:mb-6">
          <CardHeader>
              <CardTitle className="flex items-center gap-2 text-base">
                <RadioTowerIcon className="size-4" />
                Global Proxy
              </CardTitle>
              <CardDescription>
                All Agent<b>target traffic</b> egresses through this proxy (hide source IP / use jump box). Supports <b>http / https / socks5</b>, may include <code>user:pass</code> auth.
                Empty = direct connection.
                <br />
                When <b>traffic capture</b> is enabled, it serves as the <b>upstream</b> of the recording proxy (traffic is still fully logged, then egresses through this proxy); when capture is off, bash / WebFetch egress is injected directly into the Agent. Independent of web search proxy and LLM proxy.
                <br />
                <b>Tip</b>: socks5 relies on command-line tool support for <code>ALL_PROXY</code> when <b>capture is closed</b> (curl works, some tools may ignore it); if mainly using socks5, enable traffic capture — this path is dialed by MITM itself, tools are unaware, stable.
              </CardDescription>
          </CardHeader>
          <CardContent className="flex flex-col gap-2">
            <Label htmlFor="global-proxy" className="text-sm font-normal text-muted-foreground">
              Proxy address
            </Label>
            <div className="flex items-center gap-2">
              <Input
                id="global-proxy"
                autoComplete="off"
                placeholder="socks5://user:pass@host:1080 or http://host:port (empty=direct)"
                value={globalProxyInput}
                disabled={!loaded || savingGlobalProxy}
                onChange={(e) => setGlobalProxyInput(e.target.value)}
              />
              <Button type="button" onClick={saveGlobalProxy} disabled={!loaded || savingGlobalProxy}>
                Save
              </Button>
            </div>
            <p className="text-muted-foreground text-xs">
              {globalProxyInput.trim() ? "Configured · All target traffic egresses through this proxy" : "Not configured · Target traffic connects directly"}
            </p>
          </CardContent>
        </Card>

        <Card className="mb-4 break-inside-avoid md:mb-6">
          <CardHeader>
              <CardTitle className="flex items-center gap-2 text-base">
                <ShieldAlertIcon className="size-4" />
                Operation Constraint Injection
              </CardTitle>
              <CardDescription>
                When enabled, each task's <b>operation constraints</b> (allow/deny entries maintained in the task overview "Operation Constraints")
                are appended to the corresponding Agent's system prompt, to frame exploration boundaries (e.g. "test current port only", "no brute force").
                <br />
                Can be controlled separately for injection into <b>planner</b> and <b>worker</b>;
                both on by default. Takes effect immediately (next read), no Agent rebuild needed. Disabled means the Agent no longer sees constraints.
              </CardDescription>
          </CardHeader>
          <CardContent className="flex flex-col gap-4">
            <div className="flex items-center justify-between gap-4">
                <Label htmlFor="inject-planner" className="text-sm font-normal text-muted-foreground">
                  Inject planner (planner) {injectPlanner ? " · Enabled" : " · Disabled"}
                </Label>
              <Switch
                id="inject-planner"
                checked={injectPlanner}
                disabled={!loaded}
                onCheckedChange={toggleInjectPlanner}
              />
            </div>
            <div className="flex items-center justify-between gap-4">
                <Label htmlFor="inject-worker" className="text-sm font-normal text-muted-foreground">
                  Inject worker (worker) {injectWorker ? " · Enabled" : " · Disabled"}
                </Label>
              <Switch
                id="inject-worker"
                checked={injectWorker}
                disabled={!loaded}
                onCheckedChange={toggleInjectWorker}
              />
            </div>
          </CardContent>
        </Card>

        <Card className="mb-4 break-inside-avoid md:mb-6">
          <CardHeader>
              <CardTitle className="flex items-center gap-2 text-base">
                <FlaskConicalIcon className="size-4" />
                Experimental Features
              </CardTitle>
              <CardDescription>
                Mechanisms still under validation, default off. May change Agent behavior or affect stability; enable only after understanding impact.
                <br />
                <b>noa context compression</b>: model actively compresses long conversation history (norma v0.4.0). After enabling, the four types of Agents integrated into the platform (
                <b>planner / worker / main Agent / conversation</b>) use noa to manage context, replacing built-in compression; compressed original text is archived under the task working directory for traceability.
                Switching takes effect immediately (for subsequently started runs), no Agent rebuild needed; disabling immediately restores built-in compression.
              </CardDescription>
          </CardHeader>
          <CardContent className="flex items-center justify-between gap-4">
              <Label htmlFor="noa-compaction" className="text-sm font-normal text-muted-foreground">
                noa context compression{noaCompaction ? " · Enabled" : " · Disabled"}
              </Label>
            <Switch
              id="noa-compaction"
              checked={noaCompaction}
              disabled={!loaded}
              onCheckedChange={toggleNoaCompaction}
            />
          </CardContent>
        </Card>

        <Card className="mb-4 break-inside-avoid md:mb-6">
          <CardHeader>
              <CardTitle className="flex items-center gap-2 text-base">
                <SearchIcon className="size-4" />
                Web Search
              </CardTitle>
              <CardDescription>
                This is the <b>master switch + source configuration</b> for web search. After enabling, you can separately choose whether to enable
                <b>web_search</b> in each Agent's configuration (returns only title/link/summary, no full text fetch; fetching is handled by WebFetch).
                Web search <b>does not go through</b> the recording proxy, independent of traffic capture.
                <br />
                Sources available: <b>DuckDuckGo (ddgs)</b> (no key needed), <b>Brave (free)</b> (requires Brave API Key),
                <b>Tavily</b> (requires Tavily API Key), or <b>DeepSeek</b> (reuses current LLM configuration). When the master switch is off,
                each Agent's web search toggle is unavailable.
              </CardDescription>
          </CardHeader>
          <CardContent className="flex flex-col gap-4">
            <div className="flex items-center justify-between gap-4">
              <Label htmlFor="web-search" className="text-sm font-normal text-muted-foreground">
                {webSearch ? "Master switch on · Can be enabled separately in each Agent configuration" : "Off · Agents cannot enable web search"}
              </Label>
              <Switch
                id="web-search"
                checked={webSearch}
                disabled={!loaded || saving}
                onCheckedChange={(v) => {
                  setWebSearch(v); // optimistic
                  saveWebSearch({ web_search_enabled: v });
                }}
              />
            </div>

            {webSearch && (
              <div className="flex items-center justify-between gap-4">
                <Label className="text-sm font-normal text-muted-foreground">Search source</Label>
                <Select
                  value={backend}
                  disabled={!loaded || saving}
                  onValueChange={(v) => {
                    setBackend(v); // optimistic
                    saveWebSearch({ web_search_backend: v });
                  }}
                >
                  <SelectTrigger className="w-48 shrink-0">
                    <SelectValue placeholder="Select source" />
                  </SelectTrigger>
                  <SelectContent>
                    <SelectItem value="ddgs">DuckDuckGo (ddgs · Free, no key)</SelectItem>
                    <SelectItem value="brave-free">Brave (Free · Key required)</SelectItem>
                    <SelectItem value="tavily">Tavily (Key required)</SelectItem>
                    <SelectItem value="deepseek">DeepSeek (Official)</SelectItem>
                  </SelectContent>
                </Select>
              </div>
            )}

            {webSearch && backend === "deepseek" && (
              <div className="border-border/60 bg-muted/30 flex flex-col gap-2 rounded-md border p-3">
                <p className="text-sm font-medium">DeepSeek official web search</p>
                <p className="text-muted-foreground text-xs leading-relaxed">
                  This source directly reuses <b>the currently active LLM configuration</b>. Therefore it
                  <b>only supports DeepSeek official models</b>, and this configuration
                  <b>must use the anthropic protocol</b>
                  — DeepSeek's OpenAI protocol endpoint does not support server-side search. Switching LLM configuration may invalidate this source.
                </p>
                <p className="text-muted-foreground text-xs leading-relaxed">
                  Unlike other sources, search is executed by <b>DeepSeek server-side</b>: each search consumes an additional model call (incurring token
                  cost), search requests <b>do not go through the exit proxy above</b>, and also <b>do not leave traffic traces</b>; returned results <b>only have title and link</b>
                  (no summary); WebFetch is used to fetch full text when needed.
                </p>
                <p className="text-muted-foreground text-xs leading-relaxed">
                  Whether the above conditions are met is up to you to confirm; the system does not intercept; use the "Test Search" button below to actually run once to verify.
                </p>
              </div>
            )}

            {webSearch && backend === "brave-free" && (
              <div className="flex flex-col gap-2">
                <Label htmlFor="brave-key" className="text-sm font-normal text-muted-foreground">
                  Brave Search API Key
                  {braveKeySet && <span className="ml-2 text-xs text-emerald-500">Configured</span>}
                </Label>
                <div className="flex items-center gap-2">
                  <Input
                    id="brave-key"
                    type="password"
                    autoComplete="off"
                    placeholder={braveKeySet ? "Configured (leave blank to keep unchanged)" : "Enter Brave API Key"}
                    value={braveKeyInput}
                    disabled={!loaded || savingKey}
                    onChange={(e) => setBraveKeyInput(e.target.value)}
                  />
                  <Button
                    type="button"
                    onClick={saveBraveKey}
                    disabled={!loaded || savingKey || braveKeyInput.trim() === ""}
                  >
                    Save
                  </Button>
                </div>
                  {braveNeedsKey && (
                    <p className="text-xs text-amber-500">
                      Brave selected but Key not yet configured — search tool will not be enabled until Key is saved.
                    </p>
                  )}
                  <p className="text-muted-foreground text-xs">
                    Free tier quota approx. 2,000/month. Get Key at https://brave.com/search/api/.
                  </p>
              </div>
            )}

            {webSearch && backend === "tavily" && (
              <div className="flex flex-col gap-2">
                <Label htmlFor="tavily-key" className="text-sm font-normal text-muted-foreground">
                  Tavily Search API Key
                  {tavilyKeySet && <span className="ml-2 text-xs text-emerald-500">Configured</span>}
                </Label>
                <div className="flex items-center gap-2">
                  <Input
                    id="tavily-key"
                    type="password"
                    autoComplete="off"
                    placeholder={tavilyKeySet ? "Configured (leave blank to keep unchanged)" : "Enter Tavily API Key (tvly-…)"}
                    value={tavilyKeyInput}
                    disabled={!loaded || savingTavilyKey}
                    onChange={(e) => setTavilyKeyInput(e.target.value)}
                  />
                  <Button
                    type="button"
                    onClick={saveTavilyKey}
                    disabled={!loaded || savingTavilyKey || tavilyKeyInput.trim() === ""}
                  >
                    Save
                  </Button>
                </div>
                {webSearch && backend === "tavily" && !tavilyKeySet && (
                  <p className="text-xs text-amber-500">
                    Tavily selected but Key not yet configured — search tool will not be enabled until Key is saved.
                  </p>
                )}
                  <p className="text-muted-foreground text-xs">Register at https://tavily.com to get API Key.</p>
              </div>
            )}

            {webSearch && (
              <div className="flex flex-col gap-2">
                <Label htmlFor="ws-proxy" className="text-sm font-normal text-muted-foreground">
                  Exit Proxy (optional)
                </Label>
                <div className="flex items-center gap-2">
                  <Input
                    id="ws-proxy"
                    autoComplete="off"
                    placeholder="http://host:port or socks5://host:port (empty=direct)"
                    value={proxyInput}
                    disabled={!loaded || savingProxy}
                    onChange={(e) => setProxyInput(e.target.value)}
                  />
                  <Button type="button" onClick={saveProxy} disabled={!loaded || savingProxy}>
                    Save
                  </Button>
                </div>
                <p className="text-muted-foreground text-xs">
                  Independent exit proxy, used only to access search endpoints (VPN/SOCKS, etc.). Unrelated to the MITM proxy for recording traffic;
                  use this proxy to access when network is unreachable.
                </p>
              </div>
            )}

            {webSearch && (
              <div className="flex items-center justify-between gap-4 border-t pt-4">
                <p className="text-muted-foreground text-xs">
                  Actually search once "test" with current configuration (source + proxy + Key) to verify availability.
                </p>
                <Button
                  type="button"
                  variant="outline"
                  onClick={runTest}
                  disabled={!loaded || testing}
                  className="shrink-0"
                >
                  {testing ? "Testing…" : "Test Search"}
                </Button>
              </div>
            )}
          </CardContent>
        </Card>

        <Card className="mb-4 break-inside-avoid md:mb-6">
          <CardHeader>
              <CardTitle className="flex items-center gap-2 text-base">
                <RadioTowerIcon className="size-4" />
                Custom Script · Python Interpreter
              </CardTitle>
              <CardDescription>
                Custom <b>script</b> type tools use it to run Python. Detected automatically on boot (python3 priority); you can manually enter the absolute path of
                venv / specific version here, empty means auto-detect at runtime.
              </CardDescription>
          </CardHeader>
          <CardContent className="flex flex-col gap-3">
            <div className="flex items-center gap-2">
              <Input
                className="font-mono text-sm"
                placeholder="/usr/bin/python3 (empty=auto-detect)"
                value={pyInterp}
                disabled={!loaded || saving}
                onChange={(e) => setPyInterp(e.target.value)}
              />
              <Button variant="outline" onClick={detectPython} disabled={!loaded || saving}>
                Redetect
              </Button>
              <Button onClick={savePython} disabled={!loaded || saving}>
                Save
              </Button>
            </div>
          </CardContent>
        </Card>

        <Card className="mb-4 break-inside-avoid md:mb-6">
          <CardHeader>
              <CardTitle className="flex items-center gap-2 text-base">
                <CpuIcon className="size-4" />
                Work Concurrency · Work Agent Count
              </CardTitle>
              <CardDescription>
                Number of work agents running concurrently per task (default 3). Higher value means more concurrent probing but higher consumption. After change
                <b>takes effect for subsequently started tasks</b>, running tasks are not affected.
              </CardDescription>
          </CardHeader>
          <CardContent className="flex flex-col gap-3">
            <div className="flex items-center gap-2">
              <Input
                type="number"
                min={1}
                className="w-32 font-mono text-sm"
                placeholder="3"
                value={workers}
                disabled={!loaded || savingWorkers}
                onChange={(e) => setWorkers(e.target.value)}
              />
              <Button onClick={saveWorkers} disabled={!loaded || savingWorkers}>
                Save
              </Button>
            </div>
          </CardContent>
        </Card>

        <Card className="mb-4 break-inside-avoid md:mb-6">
          <CardHeader>
              <CardTitle className="flex items-center gap-2 text-base">
                <KeyboardIcon className="size-4" />
                Session Input Send Keybinding
              </CardTitle>
              <CardDescription>
                Shared by conversation page and task details main Agent conversation input box; takes effect immediately after selection, no save needed.
                <br />
                This preference <b>exists only in this browser</b>, not synced with account; needs to be re-set after switching browser or clearing site data.
              </CardDescription>
          </CardHeader>
          <CardContent className="flex items-center justify-between gap-4">
            <Label htmlFor="chat-send-mode" className="text-sm font-normal text-muted-foreground">
              Send Method
            </Label>
            <Select value={sendMode} onValueChange={(v) => setChatSendMode(v as ChatSendMode)}>
              <SelectTrigger id="chat-send-mode" className="w-72">
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                {CHAT_SEND_MODE_OPTIONS.map((option) => (
                  <SelectItem key={option.value} value={option.value}>
                    {option.label}
                  </SelectItem>
                ))}
              </SelectContent>
            </Select>
          </CardContent>
        </Card>
      </div>
    </div>
  );
}
