"use client";

import * as React from "react";

import {
  EyeIcon,
  GitCompareIcon,
  InfoIcon,
  PencilIcon,
  RotateCcwIcon,
  SaveIcon,
  Trash2Icon,
  XIcon,
} from "lucide-react";
import { useTranslation } from "react-i18next";
import { toast } from "sonner";

import { Markdown } from "@/components/markdown";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Checkbox } from "@/components/ui/checkbox";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogHeader,
  DialogTitle,
  DialogTrigger,
} from "@/components/ui/dialog";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";
import { Separator } from "@/components/ui/separator";
import { Switch } from "@/components/ui/switch";
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs";
import { Textarea } from "@/components/ui/textarea";
import { Tooltip, TooltipContent, TooltipTrigger } from "@/components/ui/tooltip";
import { api } from "@/lib/api";
import type {
  Agent,
  AgentDetail,
  AgentTrigger,
  MCPServer,
  PromptVar,
  PromptVersion,
  Settings,
  SkillItem,
  Tool,
} from "@/lib/types";
import { cn } from "@/lib/utils";

// Traffic tools are host tools gated by the global traffic capture switch: bindable, but
// only usable when capture is on. Keep this list in sync with traffic.SeedToolMetas.
const TRAFFIC_TOOL_KEYS = new Set(["traffic_search", "traffic_get"]);

// AgentEditor is the tabbed editor for one agent, used inside the agents-page
// drawer (and reused full-page for deep links). Tabs: Configuration & Prompt / MCP / Skill /
// Tools. Config + prompt save as before; visibility + tool bindings toggle live.
export function AgentEditor({ agentKey, onSaved }: { agentKey: string; onSaved?: () => void }) {
  const { t } = useTranslation();
  const [detail, setDetail] = React.useState<AgentDetail | null>(null);
  const [versions, setVersions] = React.useState<PromptVersion[]>([]);
  const [variables, setVariables] = React.useState<PromptVar[]>([]);
  const [mcp, setMcp] = React.useState<MCPServer[]>([]);
  const [skills, setSkills] = React.useState<SkillItem[]>([]);
  const [tools, setTools] = React.useState<Tool[]>([]);
  const [loaded, setLoaded] = React.useState(false);
  const [viewVer, setViewVer] = React.useState<PromptVersion | null>(null);
  const [diffVer, setDiffVer] = React.useState<PromptVersion | null>(null);

  const [prompt, setPrompt] = React.useState("");
  const [mcpVisible, setMcpVisible] = React.useState<number[]>([]);
  const [skillVisible, setSkillVisible] = React.useState<string[]>([]);
  const [preview, setPreview] = React.useState("");
  const [maxTurns, setMaxTurns] = React.useState("0");
  const [runSecs, setRunSecs] = React.useState("600");
  // "" = follow (unbound); otherwise the profile id string
  const [llmProfileId, setLlmProfileId] = React.useState("");
  const [llmProfiles, setLlmProfiles] = React.useState<NonNullable<AgentDetail["llm_profiles"]>>([]);
  const [webSearch, setWebSearch] = React.useState(false);
  const [interactiveShell, setInteractiveShell] = React.useState(false);
  const [wrapup, setWrapup] = React.useState("");
  const [wrapupDefault, setWrapupDefault] = React.useState("");
  const [wrapupTurns, setWrapupTurns] = React.useState("0");
  const [wrapupTurnsDefault, setWrapupTurnsDefault] = React.useState(5);
  // Task-level timeout wrap-up words (only worker/planner)
  const [ttSupported, setTtSupported] = React.useState(false);
  const [ttWrapup, setTtWrapup] = React.useState("");
  const [ttWrapupDefault, setTtWrapupDefault] = React.useState("");
  const [ttTurns, setTtTurns] = React.useState("0");
  const [ttTurnsDefault, setTtTurnsDefault] = React.useState(5);
  const [settings, setSettings] = React.useState<Settings | null>(null);

  React.useEffect(() => {
    api
      .mcpServers()
      .then(setMcp)
      .catch(() => {});
    api
      .skills()
      .then(setSkills)
      .catch(() => {});
    api
      .tools()
      .then(setTools)
      .catch(() => {});
    api
      .settings()
      .then(setSettings)
      .catch(() => {});
  }, []);
  // global gates: traffic tools need traffic capture, web search needs the master switch.
  const captureOn = !!settings?.traffic_capture;
  const webSearchGlobalOn = !!settings?.web_search_enabled;

  const reload = React.useCallback(() => {
    api
      .getAgent(agentKey)
      .then((d) => {
        setDetail(d);
        setPrompt(d.prompt ?? "");
        setVariables(d.variables ?? []);
        setVersions(d.versions ?? []);
        setMcpVisible(d.visibility?.mcp ?? []);
        setSkillVisible(d.visibility?.skill ?? []);
        setMaxTurns(String(d.agent?.max_turns ?? 0));
        setRunSecs(String(d.agent?.run_seconds ?? 600));
        setLlmProfileId(d.agent?.llm_profile_id != null ? String(d.agent.llm_profile_id) : "");
        setLlmProfiles(d.llm_profiles ?? []);
        setWebSearch(!!d.agent?.web_search);
        setInteractiveShell(!!d.agent?.interactive_shell);
        setWrapup(d.wrapup_prompt ?? "");
        setWrapupDefault(d.wrapup_default ?? "");
        setWrapupTurns(String(d.wrapup_max_turns ?? 0));
        setWrapupTurnsDefault(d.wrapup_max_turns_default ?? 5);
        setTtSupported(!!d.task_timeout_wrapup_supported);
        setTtWrapup(d.task_timeout_wrapup_prompt ?? "");
        setTtWrapupDefault(d.task_timeout_wrapup_default ?? "");
        setTtTurns(String(d.task_timeout_wrapup_max_turns ?? 0));
        setTtTurnsDefault(d.task_timeout_wrapup_max_turns_default ?? 5);
      })
      .catch(() => setDetail(null))
      .finally(() => setLoaded(true));
  }, [agentKey]);
  React.useEffect(() => {
    reload();
  }, [reload]);

  async function doPreview() {
    try {
      const r = await api.previewAgentPrompt(agentKey, prompt);
      setPreview(r.error ? t("agent_editor.preview_error", { error: r.error }) : r.rendered);
    } catch (e) {
      setPreview(t("agent_editor.preview_failed", { error: (e as Error).message }));
    }
  }
  async function savePrompt() {
    try {
      const r = await api.saveAgentPrompt(agentKey, prompt);
      toast.success(t("agent_editor.toast.prompt_saved", { version: r.version }));
      reload();
      onSaved?.();
    } catch (e) {
      toast.error(t("agent_editor.toast.prompt_save_failed", { error: (e as Error).message }));
    }
  }
  async function resetPrompt() {
    try {
      const r = await api.resetAgentPrompt(agentKey);
      toast.success(t("agent_editor.toast.prompt_reset_success", { version: r.version }));
      reload();
    } catch (e) {
      toast.error(t("agent_editor.toast.prompt_reset_failed", { error: (e as Error).message }));
    }
  }
  async function saveWrapup() {
    try {
      const turns = Math.max(0, Math.floor(Number(wrapupTurns) || 0));
      await api.saveAgentWrapup(agentKey, wrapup, turns);
      toast.success(
        wrapup.trim() || turns > 0 ? t("agent_editor.toast.wrapup_saved") : t("agent_editor.toast.wrapup_cleared"),
      );
      reload();
    } catch (e) {
      toast.error(t("agent_editor.toast.wrapup_save_failed", { error: (e as Error).message }));
    }
  }
  async function resetWrapup() {
    try {
      await api.resetAgentWrapup(agentKey);
      toast.success(t("agent_editor.toast.wrapup_reset_success"));
      reload();
    } catch (e) {
      toast.error(t("agent_editor.toast.wrapup_reset_failed", { error: (e as Error).message }));
    }
  }
  async function saveTaskTimeoutWrapup() {
    try {
      const turns = Math.max(0, Math.floor(Number(ttTurns) || 0));
      await api.saveAgentTaskTimeoutWrapup(agentKey, ttWrapup, turns);
      toast.success(t("agent_editor.toast.tt_wrapup_saved"));
      reload();
    } catch (e) {
      toast.error(t("agent_editor.toast.tt_wrapup_save_failed", { error: (e as Error).message }));
    }
  }
  async function resetTaskTimeoutWrapup() {
    try {
      await api.resetAgentTaskTimeoutWrapup(agentKey);
      toast.success(t("agent_editor.toast.tt_wrapup_reset_success"));
      reload();
    } catch (e) {
      toast.error(t("agent_editor.toast.tt_wrapup_reset_failed", { error: (e as Error).message }));
    }
  }
  async function saveConfig() {
    try {
      // Only submit fields actually shown for this agent, to avoid overriding
      // unseen items (e.g. goals' max_turns) with defaults.
      const patch: Parameters<typeof api.saveAgentConfig>[1] = {
        llm_profile_id: llmProfileId === "" ? null : Number(llmProfileId),
      };
      if (showConfig) {
        patch.max_turns = Math.max(0, Math.floor(Number(maxTurns) || 0));
        patch.run_seconds = Math.max(0, Math.floor(Number(runSecs) || 0));
      }
      if (showWebSearch) patch.web_search = webSearch;
      if (showInteractiveShell) patch.interactive_shell = interactiveShell;
      await api.saveAgentConfig(agentKey, patch);
      toast.success(t("agent_editor.toast.config_saved"));
      reload();
    } catch (e) {
      toast.error(t("agent_editor.toast.config_save_failed", { error: (e as Error).message }));
    }
  }
  // applyVis optimistically updates, persists, and toasts success/failure. On
  // failure it reverts to the prior selection so the UI never lies about state.
  async function applyVis(nextMcp: number[], nextSkill: string[], okMsg: string) {
    const prevMcp = mcpVisible;
    const prevSkill = skillVisible;
    setMcpVisible(nextMcp);
    setSkillVisible(nextSkill);
    try {
      await api.setAgentVisibility(agentKey, nextMcp, nextSkill);
      toast.success(okMsg);
      onSaved?.(); // refresh the list so the card's MCP/Skill counts stay in sync
    } catch (e) {
      setMcpVisible(prevMcp);
      setSkillVisible(prevSkill);
      toast.error(t("agent_editor.toast.save_failed", { error: (e as Error).message }));
    }
  }
  function toggleMcp(id: number) {
    const on = mcpVisible.includes(id);
    const name = mcp.find((m) => m.id === id)?.name ?? String(id);
    applyVis(
      on ? mcpVisible.filter((x) => x !== id) : [...mcpVisible, id],
      skillVisible,
      t("agent_editor.toast.mcp_toggled", { enabled: on ? "disabled" : "enabled", name }),
    );
  }
  function toggleSkill(name: string) {
    const on = skillVisible.includes(name);
    applyVis(
      mcpVisible,
      on ? skillVisible.filter((x) => x !== name) : [...skillVisible, name],
      t("agent_editor.toast.skill_toggled", { enabled: on ? "disabled" : "enabled", name }),
    );
  }
  async function toggleTool(tool: Tool) {
    const on = tool.agents.includes(agentKey);
    const nextAgents = on ? tool.agents.filter((k) => k !== agentKey) : [...tool.agents, agentKey];
    // optimistic update
    setTools((ts) => ts.map((x) => (x.key === tool.key ? { ...x, agents: nextAgents } : x)));
    try {
      await api.saveTool(tool.key, {
        description: tool.description,
        schema: tool.schema,
        agents: nextAgents,
        enabled: tool.enabled,
      });
      toast.success(t("agent_editor.toast.tool_bound", { bound: on ? "unbound" : "bound", key: tool.key }));
      onSaved?.(); // refresh the list so the card's tool count stays in sync
    } catch (e) {
      toast.error(t("agent_editor.toast.tool_save_failed", { error: (e as Error).message }));
      reload();
      api
        .tools()
        .then(setTools)
        .catch(() => {});
    }
  }

  if (loaded && !detail) {
    return (
      <div className="text-muted-foreground p-6 text-center text-sm">{t("agent_editor.not_found", { agentKey })}</div>
    );
  }
  // config is meaningless for the conversational main agent and the fixed-budget
  // goals decomposer; every other agent (workers, custom assistants) honors it.
  const showConfig = agentKey !== "mainagent" && agentKey !== "goals";
  // web search applies to every conversational/executing agent except the one-shot
  // goals decomposer; it's gated by the global master switch.
  const showWebSearch = agentKey !== "goals";
  // interactive shell (persistent PTY session tool family) is open to all agents except goals; no global gate.
  const showInteractiveShell = agentKey !== "goals";
  // every agent (including goals/mainagent) runs on some LLM, so "default model" binding is open to all agents.
  const showLLM = true;
  // triggers (P3) only attach to custom agents.
  const isCustom = !!detail && !detail.agent?.builtin;

  return (
    <Tabs defaultValue="prompt" className="flex min-h-0 flex-1 flex-col">
      <TabsList className="mx-4 mt-2 w-fit">
        <TabsTrigger value="prompt">{t("agent_editor.tabs.prompt")}</TabsTrigger>
        <TabsTrigger value="wrapup">{t("agent_editor.tabs.wrapup")}</TabsTrigger>
        <TabsTrigger value="mcp">{t("agent_editor.tabs.mcp")}</TabsTrigger>
        <TabsTrigger value="skill">{t("agent_editor.tabs.skill")}</TabsTrigger>
        <TabsTrigger value="tools">{t("agent_editor.tabs.tools")}</TabsTrigger>
        {isCustom && <TabsTrigger value="triggers">{t("agent_editor.tabs.triggers")}</TabsTrigger>}
      </TabsList>

      {/* Configuration + Prompt */}
      <TabsContent value="prompt" className="min-h-0 flex-1 overflow-y-auto px-4 pb-4">
        <div className="grid gap-4">
          {(showLLM || showConfig || showWebSearch || showInteractiveShell) && (
            <div className="grid gap-3 rounded-md border p-3">
              {showLLM && (
                <div className="grid gap-1.5">
                  <Label htmlFor="llm-profile" className="text-xs">
                    {t("agent_editor.llm_profile.label")}
                  </Label>
                  <div className="flex flex-wrap items-center gap-3">
                    <Select
                      value={llmProfileId || "__follow__"}
                      onValueChange={(v) => setLlmProfileId(v === "__follow__" ? "" : v)}
                    >
                      <SelectTrigger id="llm-profile" className="h-8 w-72">
                        <SelectValue />
                      </SelectTrigger>
                      <SelectContent>
                        <SelectItem value="__follow__">{t("agent_editor.llm_profile.follow")}</SelectItem>
                        {llmProfiles.map((p) => (
                          <SelectItem key={p.id} value={String(p.id)}>
                            {p.name} （{p.model}）{p.is_default ? ` · ${t("agent_editor.llm_profile.default")}` : ""}
                          </SelectItem>
                        ))}
                      </SelectContent>
                    </Select>
                    <span className="text-muted-foreground max-w-md text-xs">{t("agent_editor.llm_profile.desc")}</span>
                  </div>
                </div>
              )}
              <div className="flex flex-wrap items-end gap-3">
                {showConfig && (
                  <>
                    <div className="grid gap-1.5">
                      <Label htmlFor="max-turns" className="text-xs">
                        {t("agent_editor.max_turns.label")}
                      </Label>
                      <Input
                        id="max-turns"
                        type="number"
                        min={0}
                        className="h-8 w-32"
                        value={maxTurns}
                        onChange={(e) => setMaxTurns(e.target.value)}
                      />
                    </div>
                    <div className="grid gap-1.5">
                      <Label htmlFor="run-seconds" className="text-xs">
                        {t("agent_editor.run_seconds.label")}
                      </Label>
                      <Input
                        id="run-seconds"
                        type="number"
                        min={0}
                        className="h-8 w-32"
                        value={runSecs}
                        onChange={(e) => setRunSecs(e.target.value)}
                      />
                    </div>
                  </>
                )}
                <Button size="sm" variant="outline" onClick={saveConfig}>
                  <SaveIcon /> {t("agent_editor.save_config")}
                </Button>
              </div>
              {showWebSearch && (
                <div className="flex items-center gap-3 border-t pt-3">
                  <Switch
                    id="web-search"
                    checked={webSearch}
                    disabled={!webSearchGlobalOn}
                    onCheckedChange={setWebSearch}
                  />
                  <div className="grid gap-0.5">
                    <Label htmlFor="web-search" className="text-sm">
                      {t("agent_editor.web_search.label")}
                    </Label>
                    <span className="text-muted-foreground text-xs">
                      {webSearchGlobalOn
                        ? t("agent_editor.web_search.enabled_desc")
                        : t("agent_editor.web_search.disabled_desc")}
                    </span>
                  </div>
                </div>
              )}
              {showInteractiveShell && (
                <div className="flex items-center gap-3 border-t pt-3">
                  <Switch id="interactive-shell" checked={interactiveShell} onCheckedChange={setInteractiveShell} />
                  <div className="grid gap-0.5">
                    <Label htmlFor="interactive-shell" className="text-sm">
                      {t("agent_editor.interactive_shell.label")}
                    </Label>
                    <span className="text-muted-foreground text-xs">{t("agent_editor.interactive_shell.desc")}</span>
                  </div>
                </div>
              )}
            </div>
          )}

          <div className="grid gap-2">
            <Label className="text-muted-foreground text-xs">{t("agent_editor.variables.label")}</Label>
            <div className="flex flex-wrap gap-2">
              {variables.map((v) => (
                <Tooltip key={v.name}>
                  <TooltipTrigger asChild>
                    <button
                      type="button"
                      onClick={() => setPrompt((p) => `${p}{{.${v.name}}}`)}
                      className="hover:bg-muted inline-flex items-center gap-1 rounded-md border bg-muted/40 px-2 py-1 font-mono text-xs"
                    >
                      {`{{.${v.name}}}`}
                      <Badge variant="secondary" className="px-1 py-0 text-[10px]">
                        {v.source}
                      </Badge>
                    </button>
                  </TooltipTrigger>
                  <TooltipContent className="max-w-xs">
                    <p className="font-medium">{v.description}</p>
                    <p className="text-muted-foreground mt-1">Example: {v.example}</p>
                  </TooltipContent>
                </Tooltip>
              ))}
              {variables.length === 0 && (
                <span className="text-muted-foreground text-xs">{t("agent_editor.variables.none")}</span>
              )}
            </div>
          </div>

          <Textarea
            className="font-mono text-xs"
            rows={16}
            value={prompt}
            placeholder={t("agent_editor.prompt.placeholder")}
            onChange={(e) => setPrompt(e.target.value)}
          />

          <div className="flex flex-wrap gap-2">
            <Dialog>
              <DialogTrigger asChild>
                <Button variant="outline" size="sm" onClick={doPreview}>
                  <EyeIcon /> {t("agent_editor.preview")}
                </Button>
              </DialogTrigger>
              <DialogContent className="sm:max-w-2xl">
                <DialogHeader>
                  <DialogTitle>{t("agent_editor.preview_title")}</DialogTitle>
                  <DialogDescription>{t("agent_editor.preview_desc", { var: "{{.Var}}" })}</DialogDescription>
                </DialogHeader>
                <div className="max-h-[60vh] overflow-auto rounded-md border bg-muted/30 p-3">
                  <Markdown text={preview} />
                </div>
              </DialogContent>
            </Dialog>
            <Button size="sm" onClick={savePrompt}>
              <SaveIcon /> {t("agent_editor.save_version")}
            </Button>
            <Button variant="outline" size="sm" onClick={resetPrompt}>
              <RotateCcwIcon /> {t("agent_editor.reset_prompt")}
            </Button>
          </div>

          <Separator />
          <div className="grid gap-2">
            <Label className="text-muted-foreground text-xs">{t("agent_editor.version_history.label")}</Label>
            <ul className="grid gap-1">
              {versions.map((ver, i) => (
                <li
                  key={ver.version}
                  className="flex items-center gap-2 rounded-md px-1 py-0.5 text-xs hover:bg-muted/50"
                >
                  <span className="font-mono shrink-0">v{ver.version}</span>
                  {i === 0 && (
                    <Badge variant="secondary" className="px-1.5 py-0 shrink-0">
                      {t("agent_editor.version_history.current")}
                    </Badge>
                  )}
                  <span className="text-muted-foreground truncate flex-1">{ver.note}</span>
                  {ver.ts && (
                    <span className="text-muted-foreground/60 shrink-0 tabular-nums">
                      {new Date(ver.ts).toLocaleDateString("en-US", {
                        month: "2-digit",
                        day: "2-digit",
                        hour: "2-digit",
                        minute: "2-digit",
                      })}
                    </span>
                  )}
                  <Button variant="ghost" size="icon-sm" className="size-6 shrink-0" onClick={() => setViewVer(ver)}>
                    <EyeIcon className="size-3" />
                  </Button>
                  {i > 0 && versions[0] && (
                    <Button variant="ghost" size="icon-sm" className="size-6 shrink-0" onClick={() => setDiffVer(ver)}>
                      <GitCompareIcon className="size-3" />
                    </Button>
                  )}
                </li>
              ))}
              {versions.length === 0 && (
                <li className="text-muted-foreground text-xs">{t("agent_editor.version_history.no_versions")}</li>
              )}
            </ul>
          </div>

          {/* View version dialog */}
          <Dialog
            open={!!viewVer}
            onOpenChange={(o) => {
              if (!o) setViewVer(null);
            }}
          >
            <DialogContent className="sm:max-w-2xl">
              <DialogHeader>
                <DialogTitle>
                  v{viewVer?.version}
                  {viewVer?.version === versions[0]?.version && (
                    <Badge variant="secondary" className="ml-2 px-1.5 py-0 align-middle">
                      {t("agent_editor.version_history.current")}
                    </Badge>
                  )}
                </DialogTitle>
                <DialogDescription>
                  {viewVer?.note || t("agent_editor.version.no_note")}
                  {viewVer?.ts && (
                    <span className="ml-2 text-muted-foreground/60">
                      {new Date(viewVer.ts).toLocaleString("en-US")}
                    </span>
                  )}
                </DialogDescription>
              </DialogHeader>
              <pre className="bg-muted max-h-[55vh] overflow-auto whitespace-pre-wrap rounded-md p-3 font-mono text-xs">
                {viewVer?.template_text || t("agent_editor.version.empty")}
              </pre>
              <div className="flex gap-2 justify-end">
                {viewVer && viewVer.version !== versions[0]?.version && (
                  <Button
                    variant="outline"
                    size="sm"
                    onClick={() => {
                      if (viewVer) {
                        setDiffVer(viewVer);
                        setViewVer(null);
                      }
                    }}
                  >
                    <GitCompareIcon className="mr-1 size-3.5" /> {t("agent_editor.version.compare_current")}
                  </Button>
                )}
                <Button
                  size="sm"
                  onClick={() => {
                    if (viewVer) {
                      setPrompt(viewVer.template_text);
                      setViewVer(null);
                      toast.success(t("agent_editor.version.load_toast", { version: viewVer.version }));
                    }
                  }}
                >
                  {t("agent_editor.version.load_editor")}
                </Button>
              </div>
            </DialogContent>
          </Dialog>

          {/* Version comparison dialog */}
          <Dialog
            open={!!diffVer}
            onOpenChange={(o) => {
              if (!o) setDiffVer(null);
            }}
          >
            <DialogContent className="sm:max-w-3xl">
              <DialogHeader>
                <DialogTitle>
                  {t("agent_editor.version_compare.title", { from: diffVer?.version, to: versions[0]?.version })}
                </DialogTitle>
                <DialogDescription>
                  <span className="inline-flex items-center gap-3 text-xs">
                    <span className="rounded bg-red-500/15 px-1.5 py-0.5 text-red-600 dark:text-red-400">
                      - {t("agent_editor.version_compare.deleted")}
                    </span>
                    <span className="rounded bg-green-500/15 px-1.5 py-0.5 text-green-600 dark:text-green-400">
                      + {t("agent_editor.version_compare.added")}
                    </span>
                  </span>
                </DialogDescription>
              </DialogHeader>
              <DiffView oldText={diffVer?.template_text ?? ""} newText={versions[0]?.template_text ?? ""} />
            </DialogContent>
          </Dialog>
        </div>
      </TabsContent>

      {/* Wrap-up Prompt */}
      <TabsContent value="wrapup" className="min-h-0 flex-1 overflow-y-auto px-4 pb-4">
        <div className="grid gap-3">
          <p className="text-muted-foreground text-xs leading-relaxed">
            When this Agent is terminated due to <b>timeout</b> or <b>steps exhausted</b>, the system injects this
            "Wrap-up Prompt" to run a wrap-up round: first write back identified but not yet persisted content, then
            output a summary sentence (avoid unfinished ending). Leave blank to use built-in default.
          </p>
          <div className="flex items-center gap-2">
            <Label className="text-xs">{t("agent_editor.wrapup.label")}</Label>
            {wrapup.trim() ? (
              <Badge variant="secondary" className="px-1.5 py-0">
                {t("agent_editor.wrapup.custom")}
              </Badge>
            ) : (
              <Badge variant="outline" className="px-1.5 py-0">
                {t("agent_editor.wrapup.default")}
              </Badge>
            )}
          </div>
          <Textarea
            className="font-mono text-xs"
            rows={10}
            value={wrapup}
            placeholder={wrapupDefault || t("agent_editor.wrapup.placeholder")}
            onChange={(e) => setWrapup(e.target.value)}
          />
          <div className="grid gap-1.5">
            <Label htmlFor="wrapup-turns" className="text-xs">
              {t("agent_editor.wrapup_turns.label", { default: wrapupTurnsDefault })}
            </Label>
            <Input
              id="wrapup-turns"
              type="number"
              min={0}
              className="h-8 w-32"
              value={wrapupTurns}
              onChange={(e) => setWrapupTurns(e.target.value)}
            />
          </div>
          <div className="flex gap-2">
            <Button size="sm" onClick={saveWrapup}>
              {t("common.save")}
            </Button>
            <Button size="sm" variant="outline" onClick={resetWrapup}>
              {t("common.reset")}
            </Button>
          </div>
          {wrapupDefault && (
            <>
              <Separator />
              <div className="grid gap-1.5">
                <Label className="text-muted-foreground text-xs">{t("agent_editor.wrapup.default_builtin")}</Label>
                <pre className="text-muted-foreground max-h-40 overflow-y-auto rounded-md border bg-muted/30 p-2 text-xs whitespace-pre-wrap">
                  {wrapupDefault}
                </pre>
              </div>
            </>
          )}

          {ttSupported && (
            <>
              <Separator className="my-2" />
              <p className="text-muted-foreground text-xs leading-relaxed">
                <b>Task Timeout Wrap-up</b> (two separate sets from per-run above): injected when <b>the entire task</b>{" "}
                reaches the timeout limit, about to end. Semantics are often opposite of per-run (e.g. planner: per-run
                says "don't stop, keep planning", task timeout says "stop on time, make final judgment"). Leave blank to
                use built-in default.
              </p>
              <div className="flex items-center gap-2">
                <Label className="text-xs">{t("agent_editor.task_timeout_wrapup.label")}</Label>
                {ttWrapup.trim() ? (
                  <Badge variant="secondary" className="px-1.5 py-0">
                    {t("agent_editor.wrapup.custom")}
                  </Badge>
                ) : (
                  <Badge variant="outline" className="px-1.5 py-0">
                    {t("agent_editor.wrapup.default")}
                  </Badge>
                )}
              </div>
              <Textarea
                className="font-mono text-xs"
                rows={10}
                value={ttWrapup}
                placeholder={ttWrapupDefault || t("agent_editor.task_timeout_wrapup.placeholder")}
                onChange={(e) => setTtWrapup(e.target.value)}
              />
              <div className="grid gap-1.5">
                <Label htmlFor="tt-turns" className="text-xs">
                  {t("agent_editor.task_timeout_wrapup_turns.label", { default: ttTurnsDefault })}
                </Label>
                <Input
                  id="tt-turns"
                  type="number"
                  min={0}
                  className="h-8 w-32"
                  value={ttTurns}
                  onChange={(e) => setTtTurns(e.target.value)}
                />
              </div>
              <div className="flex gap-2">
                <Button size="sm" onClick={saveTaskTimeoutWrapup}>
                  {t("common.save")}
                </Button>
                <Button size="sm" variant="outline" onClick={resetTaskTimeoutWrapup}>
                  {t("common.reset")}
                </Button>
              </div>
              {ttWrapupDefault && (
                <div className="grid gap-1.5">
                  <Label className="text-muted-foreground text-xs">{t("agent_editor.wrapup.default_builtin")}</Label>
                  <pre className="text-muted-foreground max-h-40 overflow-y-auto rounded-md border bg-muted/30 p-2 text-xs whitespace-pre-wrap">
                    {ttWrapupDefault}
                  </pre>
                </div>
              )}
            </>
          )}
        </div>
      </TabsContent>

      {/* MCP Visibility */}
      <TabsContent value="mcp" className="min-h-0 flex-1 overflow-y-auto px-4 pb-4">
        <p className="text-muted-foreground mb-3 text-xs">{t("agent_editor.mcp.desc")}</p>
        <div className="grid gap-2">
          {mcp.map((m) => (
            <label key={m.id} className="flex items-center gap-2 rounded-md border p-2 text-sm">
              <Checkbox checked={mcpVisible.includes(m.id)} onCheckedChange={() => toggleMcp(m.id)} />
              {m.name}
              <span className="text-muted-foreground ml-auto text-xs">{m.transport}</span>
            </label>
          ))}
          {mcp.length === 0 && <span className="text-muted-foreground text-xs">{t("agent_editor.mcp.none")}</span>}
        </div>
      </TabsContent>

      {/* Skill Visibility */}
      <TabsContent value="skill" className="min-h-0 flex-1 overflow-y-auto px-4 pb-4">
        <p className="text-muted-foreground mb-3 text-xs">{t("agent_editor.skill.desc")}</p>
        <div className="grid gap-2">
          {skills.map((s) => (
            <label key={s.name} className="flex items-center gap-2 rounded-md border p-2 text-sm">
              <Checkbox checked={skillVisible.includes(s.name)} onCheckedChange={() => toggleSkill(s.name)} />
              <span className="font-mono text-xs">{s.name}</span>
              {s.description && <span className="text-muted-foreground ml-auto truncate text-xs">{s.description}</span>}
            </label>
          ))}
          {skills.length === 0 && <span className="text-muted-foreground text-xs">{t("agent_editor.skill.none")}</span>}
        </div>
      </TabsContent>

      {/* Tools Binding */}
      <TabsContent value="tools" className="min-h-0 flex-1 overflow-y-auto px-4 pb-4">
        <p className="text-muted-foreground mb-3 text-xs">{t("agent_editor.tools.desc")}</p>
        <div className="grid gap-2">
          {tools.map((tool) => {
            const isTraffic = TRAFFIC_TOOL_KEYS.has(tool.key);
            const gated = isTraffic && !captureOn; // traffic tools need traffic capture on
            return (
              <label
                key={tool.key}
                className={cn("flex items-center gap-2 rounded-md border p-2 text-sm", gated && "opacity-60")}
              >
                <Checkbox
                  checked={tool.agents.includes(agentKey)}
                  disabled={gated}
                  onCheckedChange={() => toggleTool(tool)}
                />
                <span className="font-mono text-xs">{tool.key}</span>
                {isTraffic && (
                  <Badge variant="secondary" className="px-1 py-0 text-[9px]">
                    {t("agent_editor.tools.traffic_badge")}
                  </Badge>
                )}
                {!tool.enabled && (
                  <Badge variant="outline" className="text-destructive px-1 py-0 text-[9px]">
                    {t("agent_editor.tools.disabled_badge")}
                  </Badge>
                )}
                {gated ? (
                  <span className="text-muted-foreground ml-auto text-xs">{t("agent_editor.tools.gated")}</span>
                ) : (
                  tool.description && (
                    <span className="text-muted-foreground ml-auto line-clamp-1 max-w-[55%] text-xs">
                      {tool.description}
                    </span>
                  )
                )}
              </label>
            );
          })}
          {tools.length === 0 && <span className="text-muted-foreground text-xs">{t("agent_editor.tools.none")}</span>}
        </div>
      </TabsContent>

      {/* Triggers (P3, custom agents only) */}
      {isCustom && (
        <TabsContent value="triggers" className="min-h-0 flex-1 overflow-y-auto px-4 pb-4">
          <AgentTriggersTab agentKey={agentKey} agent={detail?.agent} />
        </TabsContent>
      )}
    </Tabs>
  );
}

// ---------- Diff helpers ----------

type DiffLine = { type: "same" | "add" | "del"; text: string };

function computeDiff(oldText: string, newText: string): DiffLine[] {
  const a = oldText.split("\n");
  const b = newText.split("\n");
  const m = a.length;
  const n = b.length;
  const dp: number[][] = Array.from({ length: m + 1 }, () => new Array(n + 1).fill(0));
  for (let i = 1; i <= m; i++)
    for (let j = 1; j <= n; j++)
      dp[i][j] = a[i - 1] === b[j - 1] ? dp[i - 1][j - 1] + 1 : Math.max(dp[i - 1][j], dp[i][j - 1]);
  const result: DiffLine[] = [];
  let i = m;
  let j = n;
  while (i > 0 || j > 0) {
    if (i > 0 && j > 0 && a[i - 1] === b[j - 1]) {
      result.unshift({ type: "same", text: a[i - 1] });
      i--;
      j--;
    } else if (j > 0 && (i === 0 || dp[i][j - 1] >= dp[i - 1][j])) {
      result.unshift({ type: "add", text: b[j - 1] });
      j--;
    } else {
      result.unshift({ type: "del", text: a[i - 1] });
      i--;
    }
  }
  return result;
}

function DiffView({ oldText, newText }: { oldText: string; newText: string }) {
  const lines = React.useMemo(() => computeDiff(oldText, newText), [oldText, newText]);
  return (
    <pre className="max-h-[60vh] overflow-auto rounded-md border bg-muted/30 p-2 font-mono text-xs leading-5">
      {lines.map((l, idx) => (
        <div
          key={idx}
          className={cn(
            "whitespace-pre-wrap px-1",
            l.type === "del" && "bg-red-500/15 text-red-700 dark:text-red-400",
            l.type === "add" && "bg-green-500/15 text-green-700 dark:text-green-400",
            l.type === "same" && "text-muted-foreground",
          )}
        >
          <span className="select-none mr-1 opacity-50">{l.type === "del" ? "-" : l.type === "add" ? "+" : " "}</span>
          {l.text}
        </div>
      ))}
    </pre>
  );
}

// AgentTriggersTab manages a custom agent's P3 triggers: list + add + delete.
// Each trigger fires (scheduled / finding discovered / goal met / task timeout / tool call, multi-select) → a new conversation runs
// in parallel with the base user message + auto context appended by the backend.
function AgentTriggersTab({ agentKey, agent }: { agentKey: string; agent?: Agent }) {
  const [triggers, setTriggers] = React.useState<AgentTrigger[]>([]);
  const [tools, setTools] = React.useState<Tool[]>([]);
  // Post-trigger handling strategy (per agent); initial value from agent detail, saved on change.
  const [runMode, setRunMode] = React.useState<"serial" | "parallel">(agent?.trigger_run_mode ?? "serial");
  const [mergeMode, setMergeMode] = React.useState<"by_task" | "all" | "none">(agent?.trigger_merge_mode ?? "all");
  const [maxParallel, setMaxParallel] = React.useState(String(agent?.trigger_max_parallel ?? 5));
  React.useEffect(() => {
    setRunMode(agent?.trigger_run_mode ?? "serial");
    setMergeMode(agent?.trigger_merge_mode ?? "all");
    setMaxParallel(String(agent?.trigger_max_parallel ?? 5));
  }, [agent?.trigger_run_mode, agent?.trigger_merge_mode, agent?.trigger_max_parallel]);

  async function saveBehavior(patch: {
    trigger_run_mode?: "serial" | "parallel";
    trigger_merge_mode?: "by_task" | "all" | "none";
    trigger_max_parallel?: number;
  }) {
    try {
      await api.saveAgentConfig(agentKey, patch);
    } catch (e) {
      toast.error(t("agent_editor.behavior_save_failed", { error: (e as Error).message }));
    }
  }
  const { t } = useTranslation();
  const [onInterval, setOnInterval] = React.useState(false);
  const [intervalSec, setIntervalSec] = React.useState("60");
  const [onFinding, setOnFinding] = React.useState(false);
  const [onGoalMet, setOnGoalMet] = React.useState(false);
  const [onTaskTimeout, setOnTaskTimeout] = React.useState(false);
  const [onToolCall, setOnToolCall] = React.useState(false);
  const [onTaskCreate, setOnTaskCreate] = React.useState(false);
  const [intervalMsg, setIntervalMsg] = React.useState("");
  const [findingMsg, setFindingMsg] = React.useState("");
  const [goalMsg, setGoalMsg] = React.useState("");
  const [taskTimeoutMsg, setTaskTimeoutMsg] = React.useState("");
  const [toolCallMsg, setToolCallMsg] = React.useState("");
  const [taskCreateMsg, setTaskCreateMsg] = React.useState("");
  const [toolNames, setToolNames] = React.useState<string[]>([]);
  const [saving, setSaving] = React.useState(false);
  // null = add mode; non-null = editing the trigger with this id.
  const [editingId, setEditingId] = React.useState<number | null>(null);

  const reload = React.useCallback(() => {
    api
      .agentTriggers(agentKey)
      .then(setTriggers)
      .catch(() => setTriggers([]));
  }, [agentKey]);
  React.useEffect(() => {
    reload();
  }, [reload]);
  React.useEffect(() => {
    api
      .tools()
      .then(setTools)
      .catch(() => setTools([]));
  }, []);

  function toggleTool(key: string) {
    setToolNames((prev) => (prev.includes(key) ? prev.filter((k) => k !== key) : [...prev, key]));
  }

  // resetForm clears the form and returns to "add" mode.
  function resetForm() {
    setEditingId(null);
    setOnInterval(false);
    setIntervalSec("60");
    setOnFinding(false);
    setOnGoalMet(false);
    setOnTaskTimeout(false);
    setOnToolCall(false);
    setOnTaskCreate(false);
    setIntervalMsg("");
    setFindingMsg("");
    setGoalMsg("");
    setTaskTimeoutMsg("");
    setToolCallMsg("");
    setTaskCreateMsg("");
    setToolNames([]);
  }

  // startEdit fills the form with an existing trigger, entering "edit" mode.
  function startEdit(t: AgentTrigger) {
    setEditingId(t.id);
    setOnInterval(t.interval_sec > 0);
    setIntervalSec(t.interval_sec > 0 ? String(t.interval_sec) : "60");
    setOnFinding(t.on_finding);
    setOnGoalMet(t.on_goal_met);
    setOnTaskTimeout(t.on_task_timeout);
    setOnToolCall(t.on_tool_call);
    setOnTaskCreate(t.on_task_create);
    setIntervalMsg(t.interval_message);
    setFindingMsg(t.finding_message);
    setGoalMsg(t.goal_message);
    setTaskTimeoutMsg(t.task_timeout_message);
    setToolCallMsg(t.tool_call_message);
    setTaskCreateMsg(t.task_create_message);
    setToolNames(t.tool_names ?? []);
  }

  // submit uses "add" or "save changes" depending on editingId; when editing, preserves the trigger's enabled state.
  async function submit() {
    const n = onInterval ? Math.max(1, Math.floor(Number(intervalSec) || 0)) : 0;
    if (n === 0 && !onFinding && !onGoalMet && !onTaskTimeout && !onToolCall && !onTaskCreate) {
      toast.error(t("agent_editor.trigger_at_least_one"));
      return;
    }
    if (onToolCall && toolNames.length === 0) {
      toast.error(t("agent_editor.trigger_tool_required"));
      return;
    }
    const body = {
      interval_sec: n,
      on_finding: onFinding,
      on_goal_met: onGoalMet,
      on_task_timeout: onTaskTimeout,
      on_tool_call: onToolCall,
      on_task_create: onTaskCreate,
      interval_message: intervalMsg.trim(),
      finding_message: findingMsg.trim(),
      goal_message: goalMsg.trim(),
      task_timeout_message: taskTimeoutMsg.trim(),
      tool_call_message: toolCallMsg.trim(),
      task_create_message: taskCreateMsg.trim(),
      tool_names: onToolCall ? toolNames : [],
    };
    setSaving(true);
    try {
      if (editingId != null) {
        const cur = triggers.find((x) => x.id === editingId);
        await api.updateTrigger(editingId, { ...body, enabled: cur?.enabled ?? true });
        toast.success(t("agent_editor.trigger_saved"));
      } else {
        await api.createTrigger(agentKey, { ...body, enabled: true });
        toast.success(t("agent_editor.trigger_added"));
      }
      resetForm();
      reload();
    } catch (e) {
      toast.error(t("agent_editor.trigger_save_failed", { error: (e as Error).message }));
    } finally {
      setSaving(false);
    }
  }
  async function toggleEnabled(trigger: AgentTrigger) {
    try {
      await api.updateTrigger(trigger.id, {
        enabled: !trigger.enabled,
        interval_sec: trigger.interval_sec,
        on_finding: trigger.on_finding,
        on_goal_met: trigger.on_goal_met,
        on_task_timeout: trigger.on_task_timeout,
        on_tool_call: trigger.on_tool_call,
        on_task_create: trigger.on_task_create,
        interval_message: trigger.interval_message,
        finding_message: trigger.finding_message,
        goal_message: trigger.goal_message,
        task_timeout_message: trigger.task_timeout_message,
        tool_call_message: trigger.tool_call_message,
        task_create_message: trigger.task_create_message,
        tool_names: trigger.tool_names,
      });
      reload();
    } catch (e) {
      toast.error(t("agent_editor.toast.save_failed", { error: (e as Error).message }));
    }
  }
  async function del(id: number) {
    try {
      await api.deleteTrigger(id);
      if (editingId === id) resetForm();
      reload();
    } catch (e) {
      toast.error(t("agent_editor.trigger_delete_failed", { error: (e as Error).message }));
    }
  }

  function condLabel(trigger: AgentTrigger): string {
    const parts: string[] = [];
    if (trigger.interval_sec > 0) parts.push(t("agent_editor.cond_every", { n: trigger.interval_sec }));
    if (trigger.on_finding) parts.push(t("agent_editor.cond_finding"));
    if (trigger.on_goal_met) parts.push(t("agent_editor.cond_goal"));
    if (trigger.on_task_timeout) parts.push(t("agent_editor.cond_timeout"));
    if (trigger.on_tool_call) parts.push(t("agent_editor.cond_tool_call", { n: trigger.tool_names.length }));
    if (trigger.on_task_create) parts.push(t("agent_editor.cond_task_create"));
    return parts.join(" · ") || t("agent_editor.cond_none");
  }

  return (
    <div className="grid gap-4">
      <p className="text-muted-foreground text-xs">
        Triggers let this custom Agent run automatically: each trigger <b>creates a new session run</b> (visible on
        "Chat" page). You can select multiple trigger conditions; the system automatically appends "why this trigger was
        fired + related task/finding/goal" to your base message.
      </p>

      {/* Post-trigger handling strategy */}
      <div className="grid gap-3 rounded-md border p-3">
        <Label className="text-muted-foreground text-xs">{t("agent_editor.triggers.strategy.label")}</Label>
        <div className="flex flex-wrap items-center gap-4">
          <div className="grid gap-1">
            <Label className="text-xs">{t("agent_editor.triggers.run_mode.label")}</Label>
            <Select
              value={runMode}
              onValueChange={(v) => {
                const rm = v as "serial" | "parallel";
                setRunMode(rm);
                saveBehavior({ trigger_run_mode: rm });
              }}
            >
              <SelectTrigger size="sm" className="h-8 w-40">
                <SelectValue />
              </SelectTrigger>
              <SelectContent position="popper">
                <SelectItem value="serial">{t("agent_editor.triggers.run_mode.serial")}</SelectItem>
                <SelectItem value="parallel">{t("agent_editor.triggers.run_mode.parallel")}</SelectItem>
              </SelectContent>
            </Select>
          </div>

          <div className="grid gap-1">
            <Label className="text-xs">{t("agent_editor.triggers.merge_mode.label")}</Label>
            <Select
              value={mergeMode}
              disabled={runMode === "parallel"}
              onValueChange={(v) => {
                const mm = v as "by_task" | "all" | "none";
                setMergeMode(mm);
                saveBehavior({ trigger_merge_mode: mm });
              }}
            >
              <SelectTrigger size="sm" className="h-8 w-44">
                <SelectValue />
              </SelectTrigger>
              <SelectContent position="popper">
                <SelectItem value="by_task">{t("agent_editor.triggers.merge_mode.by_task")}</SelectItem>
                <SelectItem value="all">{t("agent_editor.triggers.merge_mode.all")}</SelectItem>
                <SelectItem value="none">{t("agent_editor.triggers.merge_mode.none")}</SelectItem>
              </SelectContent>
            </Select>
          </div>

          {runMode === "parallel" && (
            <div className="grid gap-1">
              <Label htmlFor="tr-maxpar" className="text-xs">
                {t("agent_editor.triggers.max_parallel.label")}
              </Label>
              <Input
                id="tr-maxpar"
                type="number"
                min={0}
                className="h-8 w-28"
                value={maxParallel}
                onChange={(e) => setMaxParallel(e.target.value)}
                onBlur={() => {
                  const n = Math.max(0, Math.floor(Number(maxParallel) || 0));
                  setMaxParallel(String(n));
                  saveBehavior({ trigger_max_parallel: n });
                }}
              />
            </div>
          )}
        </div>
        <p className="text-muted-foreground text-xs">
          {runMode === "parallel"
            ? t("agent_editor.triggers.desc_parallel")
            : mergeMode === "by_task"
              ? t("agent_editor.triggers.desc_by_task")
              : mergeMode === "all"
                ? t("agent_editor.triggers.desc_all")
                : t("agent_editor.triggers.desc_none")}
        </p>
      </div>

          {/* Add / Edit trigger */}
      <div className="grid gap-3 rounded-md border p-3">
        <Label className="text-muted-foreground text-xs">
          {editingId != null
            ? t("agent_editor.triggers.add_edit.edit", { id: editingId })
            : t("agent_editor.triggers.add_edit.add")}
        </Label>

        {/* Scheduled */}
        <div className="grid gap-1.5">
          <label className="flex items-center gap-2 text-sm">
            <Checkbox checked={onInterval} onCheckedChange={(v) => setOnInterval(!!v)} />{" "}
            {t("agent_editor.triggers.interval")}
          </label>
          {onInterval && (
            <div className="grid gap-1.5">
              <div className="flex items-center gap-2">
                <Label htmlFor="tr-interval" className="text-xs">
                  {t("agent_editor.triggers.interval_label")}
                </Label>
                <Input
                  id="tr-interval"
                  type="number"
                  min={1}
                  className="h-8 w-24"
                  value={intervalSec}
                  onChange={(e) => setIntervalSec(e.target.value)}
                />
                <span className="text-muted-foreground text-xs">{t("agent_editor.triggers.interval_seconds")}</span>
              </div>
              <Textarea
                className="text-xs"
                rows={2}
                value={intervalMsg}
                placeholder={t("agent_editor.triggers.interval_placeholder")}
                onChange={(e) => setIntervalMsg(e.target.value)}
              />
            </div>
          )}
        </div>

        {/* finding */}
        <div className="grid gap-1.5">
          <label className="flex items-center gap-2 text-sm">
            <Checkbox checked={onFinding} onCheckedChange={(v) => setOnFinding(!!v)} />{" "}
            {t("agent_editor.triggers.finding")}
          </label>
          {onFinding && (
            <Textarea
              className="text-xs"
              rows={2}
              value={findingMsg}
              placeholder={t("agent_editor.triggers.finding_placeholder")}
              onChange={(e) => setFindingMsg(e.target.value)}
            />
          )}
        </div>

        {/* Goal met */}
        <div className="grid gap-1.5">
          <label className="flex items-center gap-2 text-sm">
            <Checkbox checked={onGoalMet} onCheckedChange={(v) => setOnGoalMet(!!v)} />{" "}
            {t("agent_editor.triggers.goal")}
          </label>
          {onGoalMet && (
            <Textarea
              className="text-xs"
              rows={2}
              value={goalMsg}
              placeholder={t("agent_editor.triggers.goal_placeholder")}
              onChange={(e) => setGoalMsg(e.target.value)}
            />
          )}
        </div>

        {/* Task timeout */}
        <div className="grid gap-1.5">
          <label className="flex items-center gap-2 text-sm">
            <Checkbox checked={onTaskTimeout} onCheckedChange={(v) => setOnTaskTimeout(!!v)} />{" "}
            {t("agent_editor.triggers.timeout")}
          </label>
          {onTaskTimeout && (
            <Textarea
              className="text-xs"
              rows={2}
              value={taskTimeoutMsg}
              placeholder={t("agent_editor.triggers.timeout_placeholder")}
              onChange={(e) => setTaskTimeoutMsg(e.target.value)}
            />
          )}
        </div>

        {/* Tool call */}
        <div className="grid gap-1.5">
          <label className="flex items-center gap-2 text-sm">
            <Checkbox checked={onToolCall} onCheckedChange={(v) => setOnToolCall(!!v)} />{" "}
            {t("agent_editor.triggers.tool_call")}
          </label>
          {onToolCall && (
            <div className="grid gap-1.5">
              <div className="text-muted-foreground text-xs">
                {t("agent_editor.triggers.tool_call_desc", { count: toolNames.length })}
              </div>
              <div className="max-h-40 overflow-y-auto rounded-md border p-2">
                {tools.length === 0 && (
                  <span className="text-muted-foreground text-xs">{t("agent_editor.triggers.tool_call_empty")}</span>
                )}
                <div className="grid gap-1">
                  {tools.map((tool) => (
                    <label key={tool.key} className="flex items-start gap-2 text-xs">
                      <Checkbox
                        className="mt-0.5"
                        checked={toolNames.includes(tool.key)}
                        onCheckedChange={() => toggleTool(tool.key)}
                      />
                      <span className="min-w-0">
                        <span className="font-medium">{tool.key}</span>
                        {tool.description && (
                          <span className="text-muted-foreground line-clamp-1"> {tool.description}</span>
                        )}
                      </span>
                    </label>
                  ))}
                </div>
              </div>
              <Textarea
                className="text-xs"
                rows={2}
                value={toolCallMsg}
                placeholder={t("agent_editor.triggers.tool_call_placeholder")}
                onChange={(e) => setToolCallMsg(e.target.value)}
              />
            </div>
          )}
        </div>

        {/* Task creation */}
        <div className="grid gap-1.5">
          <label className="flex items-center gap-2 text-sm">
            <Checkbox checked={onTaskCreate} onCheckedChange={(v) => setOnTaskCreate(!!v)} />{" "}
            {t("agent_editor.triggers.task_create")}
          </label>
          {onTaskCreate && (
            <Textarea
              className="text-xs"
              rows={2}
              value={taskCreateMsg}
              placeholder={t("agent_editor.triggers.task_create_placeholder")}
              onChange={(e) => setTaskCreateMsg(e.target.value)}
            />
          )}
        </div>

        <div className="flex items-center gap-2">
          <Button size="sm" onClick={submit} disabled={saving}>
            <SaveIcon />{" "}
            {editingId != null ? t("agent_editor.triggers.save_edit") : t("agent_editor.triggers.save_add")}
          </Button>
          {editingId != null && (
            <Button size="sm" variant="ghost" onClick={resetForm} disabled={saving}>
              <XIcon /> {t("agent_editor.triggers.cancel_edit")}
            </Button>
          )}
        </div>
      </div>

      {/* Existing triggers */}
      <div className="grid gap-2">
        <Label className="text-muted-foreground text-xs">{t("agent_editor.triggers.existing.label")}</Label>
        {triggers.length === 0 && (
          <span className="text-muted-foreground text-xs">{t("agent_editor.triggers.existing.none")}</span>
        )}
        {triggers.map((trigger) => (
          <div
            key={trigger.id}
            className={cn(
              "flex items-start gap-2 rounded-md border p-2 text-sm",
              editingId === trigger.id && "border-primary bg-primary/5",
            )}
          >
            <Switch checked={trigger.enabled} onCheckedChange={() => toggleEnabled(trigger)} className="mt-0.5" />
            <div className="min-w-0 flex-1">
              <div className="flex flex-wrap items-center gap-1.5">
                <span className="font-medium">{condLabel(trigger)}</span>
                {!trigger.enabled && (
                  <Badge variant="outline" className="text-destructive px-1 py-0 text-[9px]">
                    {t("agent_editor.triggers.existing.disabled")}
                  </Badge>
                )}
              </div>
              <div className="text-muted-foreground grid gap-0.5 text-xs">
                {trigger.interval_sec > 0 && trigger.interval_message && (
                  <div className="line-clamp-1">
                    {t("agent_editor.triggers.existing.schedule", { msg: trigger.interval_message })}
                  </div>
                )}
                {trigger.on_finding && trigger.finding_message && (
                  <div className="line-clamp-1">
                    {t("agent_editor.triggers.existing.finding", { msg: trigger.finding_message })}
                  </div>
                )}
                {trigger.on_goal_met && trigger.goal_message && (
                  <div className="line-clamp-1">
                    {t("agent_editor.triggers.existing.goal", { msg: trigger.goal_message })}
                  </div>
                )}
                {trigger.on_task_timeout && trigger.task_timeout_message && (
                  <div className="line-clamp-1">
                    {t("agent_editor.triggers.existing.timeout", { msg: trigger.task_timeout_message })}
                  </div>
                )}
                {trigger.on_task_create && trigger.task_create_message && (
                  <div className="line-clamp-1">
                    {t("agent_editor.triggers.existing.task_create", { msg: trigger.task_create_message })}
                  </div>
                )}
                {trigger.on_tool_call && (
                  <>
                    <div className="line-clamp-1">
                      {t("agent_editor.triggers.existing.tools", {
                        names: trigger.tool_names.join("、") || t("agent_editor.triggers.existing.tools_none"),
                      })}
                    </div>
                    {trigger.tool_call_message && (
                      <div className="line-clamp-1">
                        {t("agent_editor.triggers.existing.message", { msg: trigger.tool_call_message })}
                      </div>
                    )}
                  </>
                )}
              </div>
            </div>
            <Button
              variant="ghost"
              size="icon-sm"
              className="text-muted-foreground hover:text-foreground"
              onClick={() => startEdit(trigger)}
              title={t("agent_editor.triggers.existing.edit")}
            >
              <PencilIcon className="size-3.5" />
            </Button>
            <Button
              variant="ghost"
              size="icon-sm"
              className="text-muted-foreground hover:text-destructive"
              onClick={() => del(trigger.id)}
              title={t("agent_editor.triggers.existing.delete")}
            >
              <Trash2Icon className="size-3.5" />
            </Button>
          </div>
        ))}
      </div>
    </div>
  );
}
