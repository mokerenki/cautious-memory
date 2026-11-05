"use client";

import * as React from "react";

import {
  CameraIcon,
  CpuIcon,
  FlaskConicalIcon,
  KeyboardIcon,
  RadioTowerIcon,
  SearchIcon,
  SettingsIcon,
  ShieldAlertIcon,
} from "lucide-react";
import { toast } from "sonner";

import { Button } from "@/components/ui/button";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { Field, FieldContent, FieldDescription, FieldLabel } from "@/components/ui/field";
import { Input } from "@/components/ui/input";
import { Textarea } from "@/components/ui/textarea";
import { Label } from "@/components/ui/label";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";
import { Switch } from "@/components/ui/switch";
import { api } from "@/lib/api";
import { CHAT_SEND_MODE_OPTIONS, type ChatSendMode, setChatSendMode, useChatSendMode } from "@/lib/chat-send-mode";
import type { EgressReport, LLMProfile, ProxyPoolEntry, Settings, WorkerPool } from "@/lib/types";

import { useEgressIp } from "../../_components/egress-ip-banner";
import { BasicAuthCard } from "./_components/basic-auth-card";
import { UpdateCard } from "./_components/update-card";

export default function SystemSettingsPage() {
  const [credentialAgent, setCredentialAgent] = React.useState(false);
  const [credentialBinding, setCredentialBinding] = React.useState(false);
  const [savingCredentials, setSavingCredentials] = React.useState(false);
  const [intelligenceEnabled, setIntelligenceEnabled] = React.useState(false);
  const [taskAutoTitleEnabled, setTaskAutoTitleEnabled] = React.useState(false);
  const [chatAutoTitleEnabled, setChatAutoTitleEnabled] = React.useState(false);
  const [savingIntelligence, setSavingIntelligence] = React.useState(false);
  const [trafficCapture, setTrafficCapture] = React.useState(false);
  const [browserScreenshot, setBrowserScreenshot] = React.useState(false);
  const [browserScreenshotTimestamp, setBrowserScreenshotTimestamp] = React.useState(false);
  const [opsecEnabled, setOpsecEnabled] = React.useState(false);
  const [settingQuery, setSettingQuery] = React.useState("");
  const [activeCategory, setActiveCategory] = React.useState("system");
  const [agentTrafficBinding, setAgentTrafficBinding] = React.useState(false);
  const [webSearch, setWebSearch] = React.useState(false);
  const [backend, setBackend] = React.useState("ddgs");
  const [braveKeySet, setBraveKeySet] = React.useState(false);
  const [braveKeyInput, setBraveKeyInput] = React.useState("");
  const [tavilyKeySet, setTavilyKeySet] = React.useState(false);
  const [tavilyKeyInput, setTavilyKeyInput] = React.useState("");
  const [deepSeekKeySet, setDeepSeekKeySet] = React.useState(false);
  const [deepSeekKeyInput, setDeepSeekKeyInput] = React.useState("");
  const [savingDeepSeekKey, setSavingDeepSeekKey] = React.useState(false);
  const [savingTavilyKey, setSavingTavilyKey] = React.useState(false);
  const [proxyInput, setProxyInput] = React.useState("");
  const [savingProxy, setSavingProxy] = React.useState(false);
  const [globalProxyInput, setGlobalProxyInput] = React.useState("");
  const [savingGlobalProxy, setSavingGlobalProxy] = React.useState(false);
  const [proxyPool, setProxyPool] = React.useState<ProxyPoolEntry[]>([]);
  const [proxyPoolActiveId, setProxyPoolActiveId] = React.useState("");
  const [proxyPoolText, setProxyPoolText] = React.useState("");
  const [proxyPoolTimed, setProxyPoolTimed] = React.useState(false);
  const [proxyPoolInterval, setProxyPoolInterval] = React.useState("30");
  const [proxyPoolFailure, setProxyPoolFailure] = React.useState(false);
  const [proxyPoolStrategy, setProxyPoolStrategy] = React.useState<"sequential" | "random">("sequential");
  const [proxyPoolThreshold, setProxyPoolThreshold] = React.useState("3");
  const [proxyPoolCooldown, setProxyPoolCooldown] = React.useState("60");
  const [proxyPoolHealthInterval, setProxyPoolHealthInterval] = React.useState("30");
  const [proxyPoolLogs, setProxyPoolLogs] = React.useState<
    Array<{ at: string; from: string; to: string; reason: string }>
  >([]);
  const [proxyReconnectMax, setProxyReconnectMax] = React.useState("5");
  const [proxyReconnectInterval, setProxyReconnectInterval] = React.useState("30");
  const [savingEgressVisibility, setSavingEgressVisibility] = React.useState(false);
  // 出口探测：测一次真实请求，拿到"直连 / 经全局代理"两条出口 IP。
  const [egressTesting, setEgressTesting] = React.useState(false);
  const [egressReport, setEgressReport] = React.useState<EgressReport | null>(null);
  // 请求序号：改了输入/保存/切捕获之后，在途请求回来时序号已经变了，直接丢弃。
  // 不这么做会出现"结果刚被清空，又被上一次的响应填回来"。
  const egressSeq = React.useRef(0);
  // 清空结果并作废在途请求。
  const clearEgressReport = () => {
    egressSeq.current += 1;
    setEgressReport(null);
    setEgressTesting(false);
  };
  // 探测地址：留空用公网回显；隔离网填内网回显服务。
  const [egressProbeUrl, setEgressProbeUrl] = React.useState("");
  const [savingEgressProbeUrl, setSavingEgressProbeUrl] = React.useState(false);
  const [testing, setTesting] = React.useState(false);
  const [loaded, setLoaded] = React.useState(false);
  const [settingsLoaded, setSettingsLoaded] = React.useState(false);
  const [saving, setSaving] = React.useState(false);
  const [savingKey, setSavingKey] = React.useState(false);
  const [pyInterp, setPyInterp] = React.useState("");
  const [workers, setWorkers] = React.useState("3");
  const [savingWorkers, setSavingWorkers] = React.useState(false);
  const [workerPools, setWorkerPools] = React.useState<WorkerPool[]>([]);
  const [llmProfiles, setLlmProfiles] = React.useState<LLMProfile[]>([]);
  // 操作约束注入范围(默认都开)。
  const [injectPlanner, setInjectPlanner] = React.useState(true);
  const [injectWorker, setInjectWorker] = React.useState(true);
  // 实验功能:noa 上下文压缩(默认关)。
  const [noaCompaction, setNoaCompaction] = React.useState(false);
  // 纯前端偏好：不走 /api/settings，直接读写 localStorage。
  const sendMode = useChatSendMode();
  const {
    enabled: showEgressIp,
    isLoading: egressProbeLoading,
    setEnabled: setShowEgressIp,
    probeEgress,
  } = useEgressIp();

  const apply = React.useCallback((s: Settings) => {
    setCredentialAgent(!!s.credential_agent_enabled);
    setCredentialBinding(!!s.credential_traffic_binding);
    setIntelligenceEnabled(!!s.task_intelligence_enabled);
    setTaskAutoTitleEnabled(!!s.task_auto_title_enabled);
    setChatAutoTitleEnabled(!!s.chat_auto_title_enabled);
    setTrafficCapture(!!s.traffic_capture);
    setBrowserScreenshot(!!s.browser_screenshot_enabled);
    setBrowserScreenshotTimestamp(!!s.browser_screenshot_timestamp_enabled);
    setOpsecEnabled(!!s.opsec_enabled);
    setAgentTrafficBinding(!!s.agent_traffic_binding);
    setWebSearch(!!s.web_search_enabled);
    setBackend(s.web_search_backend || "ddgs");
    setBraveKeySet(!!s.brave_key_set);
    setTavilyKeySet(!!s.tavily_key_set);
    setDeepSeekKeySet(!!s.deepseek_search_key_set);
    setProxyInput(s.web_search_proxy ?? "");
    const savedProxy = s.global_proxy ?? "";
    setGlobalProxyInput(savedProxy);
    setProxyPool(s.proxy_pool ?? []);
    setProxyPoolActiveId(s.proxy_pool_active_id ?? "");
    setProxyPoolTimed(!!s.proxy_rotate_timed_enabled);
    setProxyPoolInterval(String(s.proxy_rotate_interval_seconds ?? 30));
    setProxyPoolFailure(!!s.proxy_rotate_on_failure);
    setProxyPoolStrategy(s.proxy_rotate_strategy === "random" ? "random" : "sequential");
    setProxyPoolThreshold(String(s.proxy_health_fail_threshold ?? 3));
    setProxyPoolCooldown(String(s.proxy_health_cooldown_seconds ?? 60));
    setProxyPoolHealthInterval(String(s.proxy_health_check_interval_seconds ?? 30));
    setProxyPoolLogs(s.proxy_pool_switch_log ?? []);
    setProxyReconnectMax(String(s.proxy_reconnect_max_attempts ?? 5));
    setProxyReconnectInterval(String(s.proxy_reconnect_interval_seconds ?? 30));
    setEgressProbeUrl(s.egress_probe_url ?? "");
    setPyInterp(s.python_interpreter ?? "");
    setWorkers(String(s.workers ?? 3));
    setWorkerPools(s.worker_pools ?? []);
    setInjectPlanner(s.constraints_inject_planner !== false);
    setInjectWorker(s.constraints_inject_worker !== false);
    setNoaCompaction(!!s.noa_compaction);
  }, []);

  const saveWorkers = () => {
    if (!settingsLoaded) {
      toast.error("Failed to load settings. Refresh before saving Worker configuration.");
      return;
    }
    const n = Number(workers);
    if (workerPools.length > 0) {
      const total = workerPools.reduce((sum, pool) => sum + Number(pool.workers), 0);
      if (workerPools.some((pool) => !pool.profile_id)) {
        toast.error("Select an LLM profile for each model pool.");
        return;
      }
      if (workerPools.some((pool) => !Number.isInteger(Number(pool.workers)) || Number(pool.workers) <= 0)) {
        toast.error("Each model pool must have a positive integer number of Workers.");
        return;
      }
      if (total > 50) {
        toast.error("The total number of Workers cannot exceed 50.");
        return;
      }
      const ids = workerPools.map((pool) => pool.profile_id);
      if (new Set(ids).size !== ids.length) {
        toast.error("An LLM profile cannot be selected more than once.");
        return;
      }
      setSavingWorkers(true);
      api
        .setSettings({
          workers: total,
          worker_pools: workerPools.map((pool) => ({ ...pool, workers: Number(pool.workers) })),
        })
        .then((s) => {
          apply(s);
          toast.success("Multi-model Worker pool saved. This applies to newly started tasks.");
        })
        .catch((e) => toast.error(`Save failed: ${(e as Error).message}`))
        .finally(() => setSavingWorkers(false));
      return;
    }
    if (!Number.isInteger(n) || n <= 0) {
      toast.error("Concurrency must be a positive integer.");
      return;
    }
    setSavingWorkers(true);
    api
      .setSettings({ workers: n, worker_pools: [] })
      .then((s) => {
        apply(s);
        toast.success("Worker concurrency saved. This applies to newly started tasks.");
      })
      .catch((e) => toast.error("Save failed: " + (e as Error).message))
      .finally(() => setSavingWorkers(false));
  };

  const addWorkerPool = () => setWorkerPools((pools) => [...pools, { profile_id: "", workers: 1 }]);

  const savePython = () => {
    setSaving(true);
    api
      .setSettings({ python_interpreter: pyInterp.trim() })
      .then((s) => {
        apply(s);
        toast.success("Python interpreter settings saved.");
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
      .then((s) => {
        apply(s);
        setSettingsLoaded(true);
        void api
          .proxyPool()
          .then((pool) => {
            setProxyPool(pool.entries);
            setProxyPoolActiveId(pool.active_id);
            setProxyPoolLogs(pool.switch_log);
          })
          .catch(() => undefined);
      })
      .catch(() => {
        setSettingsLoaded(false);
        toast.error("Failed to load settings. Worker configuration is locked to prevent overwriting the server's model pools.");
      })
      .finally(() => setLoaded(true));
  }, [apply]);

  React.useEffect(() => {
    api
      .llmProfiles()
      .then(setLlmProfiles)
      .catch(() => setLlmProfiles([]));
  }, []);

  const toggleTraffic = (v: boolean) => {
    setTrafficCapture(v); // optimistic
    setSaving(true);
    api
      .setSettings({ traffic_capture: v })
      .then((s) => {
        apply(s);
        // 捕获开关一变，② 的链路就变了，旧结果不能留。
        clearEgressReport();
      })
      .catch(() => setTrafficCapture(!v)) // revert on failure
      .finally(() => setSaving(false));
  };

  const toggleBrowserScreenshot = (v: boolean) => {
    setBrowserScreenshot(v);
    setSaving(true);
    api
      .setSettings({ browser_screenshot_enabled: v })
      .then((s) => {
        apply(s);
        toast.success(v ? "Browser screenshot tool enabled." : "Browser screenshot tool disabled.");
      })
      .catch((e) => {
        setBrowserScreenshot(!v);
        toast.error(`Save failed: ${(e as Error).message}`);
      })
      .finally(() => setSaving(false));
  };

  const toggleBrowserScreenshotTimestamp = (v: boolean) => {
    setBrowserScreenshotTimestamp(v);
    setSaving(true);
    api
      .setSettings({ browser_screenshot_timestamp_enabled: v })
      .then((s) => {
        apply(s);
        toast.success(v ? "Screenshot timestamps enabled." : "Screenshot timestamps disabled.");
      })
      .catch((e) => {
        setBrowserScreenshotTimestamp(!v);
        toast.error(`Save failed: ${(e as Error).message}`);
      })
      .finally(() => setSaving(false));
  };

  const toggleOpsec = (v: boolean) => {
    setOpsecEnabled(v);
    setSaving(true);
    api
      .setSettings({ opsec_enabled: v })
      .then((s) => {
        apply(s);
        toast.success(v ? "OPSEC guidelines enabled." : "OPSEC guidelines disabled.");
      })
      .catch((e) => {
        setOpsecEnabled(!v);
        toast.error(`Save failed: ${(e as Error).message}`);
      })
      .finally(() => setSaving(false));
  };

  const matchesSetting = (keywords: string) => {
    const query = settingQuery.trim().toLowerCase();
    return !query || keywords.toLowerCase().includes(query);
  };

  const isSettingVisible = (category: string, keywords: string) => {
    const query = settingQuery.trim();
    return query ? matchesSetting(keywords) : activeCategory === category;
  };

  const settingCategories = [
    { id: "system", label: "System and account", hint: "Version, login, and basic settings", icon: SettingsIcon },
    { id: "traffic", label: "Traffic and security", hint: "Traffic, proxies, screenshots, and OPSEC", icon: RadioTowerIcon },
    { id: "agent", label: "Agent behavior", hint: "Constraints, intelligence, and automation", icon: CpuIcon },
    { id: "network", label: "Network and tools", hint: "Search, scripts, and concurrency", icon: SearchIcon },
    { id: "chat", label: "Chat and interface", hint: "Input and interaction preferences", icon: KeyboardIcon },
  ] as const;

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
        toast.success(v ? "Automatic traffic linking for Agents enabled." : "Automatic traffic linking for Agents disabled.");
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
        toast.success(v ? "noa context compaction enabled for future runs." : "noa context compaction disabled. Built-in compaction restored.");
      })
      .catch((e) => {
        setNoaCompaction(!v); // revert on failure
        toast.error(`Save failed: ${(e as Error).message}`);
      });
  };

  const toggleAutoTitle = (key: "task_auto_title_enabled" | "chat_auto_title_enabled", value: boolean) => {
    const setter = key === "task_auto_title_enabled" ? setTaskAutoTitleEnabled : setChatAutoTitleEnabled;
    const patch: Partial<Settings> =
      key === "task_auto_title_enabled" ? { task_auto_title_enabled: value } : { chat_auto_title_enabled: value };
    setter(value);
    api
      .setSettings(patch)
      .then(apply)
      .catch((e) => {
        setter(!value);
        toast.error("Save failed: " + (e as Error).message);
      });
  };

  // Persist a web-search patch (enable and/or backend). Optimistic with refetch.
  const saveWebSearch = (patch: Partial<Settings>) => {
    setSaving(true);
    api
      .setSettings(patch)
      .then((s) => {
        apply(s);
        toast.success("Web search settings saved.");
      })
      .catch((e) => {
        toast.error("Save failed: " + (e as Error).message);
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
        toast.success("Brave API key saved.");
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
        toast.success("Tavily API key saved.");
      })
      .catch((e) => toast.error("Save failed: " + (e as Error).message))
      .finally(() => setSavingTavilyKey(false));
  };

  const saveDeepSeekKey = () => {
    setSavingDeepSeekKey(true);
    api.setSettings({ deepseek_search_api_key: deepSeekKeyInput }).then((s) => {
      apply(s);
      setDeepSeekKeyInput("");
      toast.success("DeepSeek Search API key saved.");
    }).catch((e) => toast.error(`Save failed: ${(e as Error).message}`))
      .finally(() => setSavingDeepSeekKey(false));
  };

  const saveProxy = () => {
    setSavingProxy(true);
    api
      .setSettings({ web_search_proxy: proxyInput.trim() })
      .then((s) => {
        apply(s);
        toast.success(proxyInput.trim() ? "Egress proxy saved." : "Egress proxy cleared; using a direct connection.");
      })
      .catch((e) => toast.error("Save failed: " + (e as Error).message))
      .finally(() => setSavingProxy(false));
  };

  const saveGlobalProxy = () => {
    if (proxyPool.length > 0) {
      toast.info("When the proxy pool is not empty, the global proxy field is display-only. Edit or switch proxies in the pool.");
      return;
    }
    setSavingGlobalProxy(true);
    api
      .setSettings({ global_proxy: globalProxyInput.trim() })
      .then((s) => {
        apply(s);
        // 保存后链路变了，旧结果（尤其"尚未保存"）立刻失效。
        clearEgressReport();
        if (showEgressIp) void probeEgress().catch(() => undefined);
        toast.success(globalProxyInput.trim() ? "Global proxy saved." : "Global proxy cleared; using a direct connection.");
      })
      .catch((e) => toast.error("Save failed: " + (e as Error).message))
      .finally(() => setSavingGlobalProxy(false));
  };

  const importProxyPool = () => {
    api
      .importProxyPool({ text: proxyPoolText })
      .then((r) => {
        setProxyPool(r.entries);
        setProxyPoolActiveId(r.active_id);
        setProxyPoolLogs(r.switch_log);
        setProxyPoolText("");
        if (r.errors.length) toast.error(r.errors.map((e) => `Line ${e.line}: ${e.error}`).join("; "));
        else toast.success("Proxy pool imported.");
      })
      .catch((e) => toast.error("Import failed: " + (e as Error).message));
  };
  const saveProxyPoolStrategy = () => {
    const interval = Number(proxyPoolInterval);
    if (!Number.isFinite(interval) || !Number.isInteger(interval) || interval < 3) {
      toast.error("The timed rotation interval must be an integer of at least 3 seconds.");
      return;
    }
    api
      .setSettings({
        proxy_rotate_timed_enabled: proxyPoolTimed,
        proxy_rotate_interval_seconds: Number(proxyPoolInterval),
        proxy_rotate_on_failure: proxyPoolFailure,
        proxy_rotate_strategy: proxyPoolStrategy,
        proxy_health_fail_threshold: Number(proxyPoolThreshold),
        proxy_health_cooldown_seconds: Number(proxyPoolCooldown),
        proxy_health_check_interval_seconds: Number(proxyPoolHealthInterval),
      })
      .then((s) => {
        apply(s);
        toast.success("Proxy pool rotation policy saved.");
      })
      .catch((e) => toast.error("Save failed: " + (e as Error).message));
  };
  const testAllProxyPool = () =>
    api
      .testAllProxyPool()
      .then((r) => {
        setProxyPool(r.entries);
        setProxyPoolActiveId(r.active_id);
        setProxyPoolLogs(r.switch_log);
        toast.success("All proxy checks completed.");
      })
      .catch((e) => toast.error("Probe failed: " + (e as Error).message));
  const testOneProxyPool = (id: string) =>
    api
      .testProxyPoolEntry(id)
      .then(() => api.proxyPool())
      .then((r) => {
        setProxyPool(r.entries);
        setProxyPoolActiveId(r.active_id);
        setProxyPoolLogs(r.switch_log);
      })
      .catch((e) => toast.error("Probe failed: " + (e as Error).message));
  const refreshProxyPool = () =>
    api.proxyPool().then((r) => {
      setProxyPool(r.entries);
      setProxyPoolActiveId(r.active_id);
      setProxyPoolLogs(r.switch_log);
    });
  const toggleProxyPoolEntry = (entry: ProxyPoolEntry) =>
    api
      .updateProxyPool(entry.id, { enabled: !entry.enabled })
      .then(() => refreshProxyPool())
      .catch((e) => toast.error("Update failed: " + (e as Error).message));
  const deleteProxyPoolEntry = (id: string) =>
    api
      .deleteProxyPool(id)
      .then(() => refreshProxyPool())
      .catch((e) => toast.error("Delete failed: " + (e as Error).message));

  const saveProxyReconnect = () => {
    api
      .setSettings({
        proxy_reconnect_max_attempts: Number(proxyReconnectMax),
        proxy_reconnect_interval_seconds: Number(proxyReconnectInterval),
      })
      .then((s) => {
        apply(s);
        toast.success("Proxy reconnect policy saved.");
      })
      .catch((e) => toast.error("Save failed: " + (e as Error).message));
  };

  const saveEgressProbeUrl = async () => {
    setSavingEgressProbeUrl(true);
    try {
      const settings = await api.setSettings({ egress_probe_url: egressProbeUrl.trim() });
      setEgressProbeUrl(settings.egress_probe_url ?? "");
      clearEgressReport();
      toast.success(settings.egress_probe_url ? "IP probe service saved." : "Default IP probe service restored.");
      if (showEgressIp) void probeEgress().catch(() => undefined);
    } catch (e) {
      toast.error(`Failed to save IP probe service: ${(e as Error).message}`);
    } finally {
      setSavingEgressProbeUrl(false);
    }
  };

  // 测表单里当前的值，不要求先保存；后端返回直连 / 经代理两条。
  const testEgress = () => {
    const seq = ++egressSeq.current;
    setEgressTesting(true);
    setEgressReport(null);
    api
      .testEgressProxy({
        global_proxy: globalProxyInput.trim(),
        resume: true,
        probe_url: egressProbeUrl.trim(),
      })
      .then((rep) => {
        if (seq !== egressSeq.current) return; // 期间改过配置，这份结果已经过期
        setEgressReport(rep);
        if (rep.ok) {
          // 先取 ②：直连那行永远成功，先命中它会把直连 IP 当成出口报出去。
          const hit =
            rep.checks.find((c) => c.key === "global" && !c.skipped && c.result.ok) ??
            rep.checks.find((c) => !c.skipped && c.result.ok);
          toast.success(hit?.result.exit_ip ? `Egress IP: ${hit.result.exit_ip}` : "Probe succeeded.");
        } else {
          toast.error("Egress probe failed.");
        }
      })
      .catch((e) => {
        if (seq === egressSeq.current) toast.error("Probe failed: " + (e as Error).message);
      })
      .finally(() => {
        if (seq === egressSeq.current) setEgressTesting(false);
      });
  };

  const toggleEgressVisibility = async (nextEnabled: boolean) => {
    setSavingEgressVisibility(true);
    try {
      await setShowEgressIp(nextEnabled);
      toast.success(nextEnabled ? "Egress IP is now shown in the header." : "Egress IP hidden from the header.");
    } catch (e) {
      toast.error("Failed to save the header egress IP setting: " + (e as Error).message);
    } finally {
      setSavingEgressVisibility(false);
    }
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
        deepseek_search_api_key: deepSeekKeyInput,
      })
      .then((r) => {
        if (r.ok) toast.success(`Search test succeeded · ${r.backend} returned ${r.count} results`);
        else toast.error("Search test failed: " + (r.error || "Unknown error"));
      })
      .catch((e) => toast.error("Search test failed: " + (e as Error).message))
      .finally(() => setTesting(false));
  };

  // brave-free selected but no key stored and none being entered → tool stays off.
  const braveNeedsKey = webSearch && backend === "brave-free" && !braveKeySet;

  const saveCredentialOptions = async (patch: Partial<Settings>) => {
    setSavingCredentials(true);
    try {
      const s = await api.setSettings(patch);
      setCredentialAgent(!!s.credential_agent_enabled);
      setCredentialBinding(!!s.credential_traffic_binding);
      setIntelligenceEnabled(!!s.task_intelligence_enabled);
      toast.success("Credential processing settings saved.");
    } catch (e) {
      toast.error("Save failed: " + (e as Error).message);
    } finally {
      setSavingCredentials(false);
    }
  };
  return (
    <div className="flex flex-1 flex-col gap-4 md:gap-6">
      <div>
        <h1 className="text-xl font-semibold tracking-tight">System settings</h1>
        <p className="text-muted-foreground text-sm">Global runtime controls</p>
        <div className="relative mt-4 max-w-xl">
          <SearchIcon className="text-muted-foreground pointer-events-none absolute top-1/2 left-3 size-4 -translate-y-1/2" />
          <Input
            value={settingQuery}
            onChange={(event) => setSettingQuery(event.target.value)}
            placeholder="Search settings, e.g. screenshots, OPSEC, proxy, traffic"
            className="pl-9"
            aria-label="Search system settings"
          />
        </div>
      </div>

      <div className="flex min-h-0 flex-1 flex-col gap-4 lg:flex-row lg:items-start">
        <aside className="shrink-0 lg:sticky lg:top-4 lg:w-64">
          <div className="rounded-xl border bg-card/60 p-2 shadow-sm">
            <div className="px-3 pb-2 pt-2 text-xs font-medium uppercase tracking-wider text-muted-foreground">
              Categories
            </div>
            <nav className="flex gap-1 overflow-x-auto lg:flex-col" aria-label="System settings categories">
              {settingCategories.map((category) => {
                const Icon = category.icon;
                const active = !settingQuery.trim() && activeCategory === category.id;
                return (
                  <button
                    key={category.id}
                    type="button"
                    onClick={() => {
                      setActiveCategory(category.id);
                      setSettingQuery("");
                    }}
                    className={`group flex min-w-max items-center gap-3 rounded-lg px-3 py-3 text-left transition-colors lg:min-w-0 ${
                      active ? "bg-primary text-primary-foreground shadow-sm" : "hover:bg-muted"
                    }`}
                  >
                    <Icon className="size-4 shrink-0" />
                    <span className="min-w-0">
                      <span className="block text-sm font-medium">{category.label}</span>
                      <span
                        className={`hidden truncate text-xs lg:block ${active ? "text-primary-foreground/75" : "text-muted-foreground"}`}
                      >
                        {category.hint}
                      </span>
                    </span>
                  </button>
                );
              })}
            </nav>
          </div>
        </aside>

        <main className="min-w-0 flex-1">
          <div className="mb-3 flex items-center justify-between gap-3 px-1">
            <div>
              <h2 className="text-base font-semibold">
                {settingQuery.trim() ? "Search results" : settingCategories.find((item) => item.id === activeCategory)?.label}
              </h2>
              <p className="text-muted-foreground text-xs">
                {settingQuery.trim()
                  ? `Settings matching “${settingQuery.trim()}”`
                  : settingCategories.find((item) => item.id === activeCategory)?.hint}
              </p>
            </div>
            {settingQuery.trim() ? (
              <Button type="button" variant="ghost" size="sm" onClick={() => setSettingQuery("")}>
                Clear search
              </Button>
            ) : null}
          </div>

          <div className="flex flex-col divide-y rounded-xl border bg-card/30 px-3 [&>[data-slot=card]]:mb-0 [&>[data-slot=card]]:rounded-none [&>[data-slot=card]]:bg-transparent [&>[data-slot=card]]:py-4 [&>[data-slot=card]]:ring-0">
            {isSettingVisible("system", "update version upgrade") ? <UpdateCard /> : null}
            {isSettingVisible("system", "basic authentication login username password") ? <BasicAuthCard /> : null}

            <Card
              className={`${isSettingVisible("traffic", "traffic capture proxy HTTP") ? "" : "hidden"} mb-4 break-inside-avoid md:mb-6`}
            >
              <CardHeader>
                <CardTitle className="flex items-center gap-2 text-base">
                  <RadioTowerIcon className="size-4" />
                  Traffic capture
                </CardTitle>
                <CardDescription>
                  When enabled, all Agent HTTP traffic is stored through the recording proxy, and the traffic_search / traffic_get
                  tools and proxy configuration are provided to Agents (including proxy instructions in their prompts).
                  <br />
                  When disabled (the default), no traffic is recorded. Agents do <b>not</b> receive proxy settings or traffic tools, and their prompts contain <b>no</b> proxy instructions. Agents are rebuilt immediately when this setting changes.
                </CardDescription>
              </CardHeader>
              <CardContent className="flex items-center justify-between gap-4">
                <Label htmlFor="traffic-capture" className="text-sm font-normal text-muted-foreground">
                  {trafficCapture ? "Enabled · recording traffic and providing proxy settings" : "Disabled · traffic is not recorded and proxy settings are not provided"}
                </Label>
                <Switch
                  id="traffic-capture"
                  checked={trafficCapture}
                  disabled={!loaded || saving}
                  onCheckedChange={toggleTraffic}
                />
              </CardContent>
            </Card>

            <Card
              className={`${isSettingVisible("traffic", "Agent automatic traffic linking packets reports evidence") ? "" : "hidden"} mb-4 break-inside-avoid md:mb-6`}
            >
              <CardHeader>
                <CardTitle className="flex items-center gap-2 text-base">
                  <RadioTowerIcon className="size-4" />
                  Automatic traffic linking for Agents
                </CardTitle>
                <CardDescription id="agent-traffic-binding-description">
                  Off by default. When enabled, the reporting Agent checks existing HTTP requests and responses when a finding is saved, links matching traffic, and then writes the report.
                  <b>Reviewing packets and additional tool calls consumes more tokens.</b>
                  <br />
                  Reports can still be submitted for TCP findings, uncaptured traffic, or when no matching traffic is found. This setting does not affect traffic capture, manual linking, or access to saved evidence. It applies to subsequent Agent runs; disabling it immediately prevents new automatic links.
                </CardDescription>
              </CardHeader>
              <CardContent className="flex items-center justify-between gap-4">
                <Label htmlFor="agent-traffic-binding" className="text-sm font-normal text-muted-foreground">
                  {agentTrafficBinding ? "Enabled · increases token usage" : "Disabled · manual linking is still available"}
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

            <Card
              className={`${isSettingVisible("traffic", "screenshots browser report time OPSEC headers proxy TLS traffic") ? "" : "hidden"} mb-4 break-inside-avoid md:mb-6`}
            >
              <CardHeader>
                <CardTitle className="flex items-center gap-2 text-base">
                  <CameraIcon className="size-4" />
                  Browser and report security
                </CardTitle>
                <CardDescription>
                  Configure report screenshots, screenshot timestamps, and OPSEC guidelines for target interactions. These controls affect subsequent Agent runs only.
                </CardDescription>
              </CardHeader>
              <CardContent className="divide-y">
                <div className="flex items-center justify-between gap-4 py-3 first:pt-0">
                  <div className="min-w-0">
                    <Label htmlFor="browser-screenshot" className="text-sm font-medium">
                      Browser screenshots
                    </Label>
                    <p className="text-muted-foreground mt-1 text-xs">
                      The reporting Agent can use the browser MCP to save screenshots for reproduction steps.
                    </p>
                  </div>
                  <Switch
                    id="browser-screenshot"
                    checked={browserScreenshot}
                    disabled={!loaded || saving}
                    onCheckedChange={toggleBrowserScreenshot}
                  />
                </div>
                <div className="flex items-center justify-between gap-4 py-3">
                  <div className="min-w-0">
                    <Label htmlFor="browser-screenshot-timestamp" className="text-sm font-medium">
                      Screenshot timestamps
                    </Label>
                    <p className="text-muted-foreground mt-1 text-xs">Show the date and time in the upper-right corner of screenshots without modifying the original files.</p>
                  </div>
                  <Switch
                    id="browser-screenshot-timestamp"
                    checked={browserScreenshotTimestamp}
                    disabled={!loaded || saving}
                    onCheckedChange={toggleBrowserScreenshotTimestamp}
                  />
                </div>
                <div className="flex items-center justify-between gap-4 py-3 last:pb-0">
                  <div className="min-w-0">
                    <Label htmlFor="opsec-enabled" className="text-sm font-medium">
                      OPSEC guidelines
                    </Label>
                    <p className="text-muted-foreground mt-1 text-xs">
                      Require execution Agents to reuse traffic and browser characteristics, limit request noise, and follow proxy and authorization boundaries.
                    </p>
                  </div>
                  <Switch
                    id="opsec-enabled"
                    checked={opsecEnabled}
                    disabled={!loaded || saving}
                    onCheckedChange={toggleOpsec}
                  />
                </div>
              </CardContent>
            </Card>

            <Card
              className={`${isSettingVisible("traffic", "global proxy egress IP SOCKS5 HTTP HTTPS") ? "" : "hidden"} mb-4 break-inside-avoid md:mb-6`}
            >
              <CardHeader>
                <CardTitle className="flex items-center gap-2 text-base">
                  <RadioTowerIcon className="size-4" />
                  Global proxy
                </CardTitle>
                <CardDescription>
                  Configure the egress proxy for Agent target traffic. Leave blank for a direct connection. Web search and LLM proxies are not affected.
                </CardDescription>
              </CardHeader>
              <CardContent className="flex flex-col gap-5">
                <section className="space-y-2">
                  <Label htmlFor="global-proxy" className="text-sm font-normal text-muted-foreground">
                    Proxy address
                  </Label>
                  <div className="flex flex-wrap items-center gap-2">
                    <Input
                      id="global-proxy"
                      className="min-w-0 flex-1"
                      autoComplete="off"
                      placeholder="proxy.example:1080 or http://host:port (leave blank for direct connection)"
                      value={globalProxyInput}
                      disabled={!loaded || savingGlobalProxy}
                      onChange={(e) => {
                        setGlobalProxyInput(e.target.value);
                        // 旧结果对应的是上一次的地址，留着会让人以为新值已经测过。
                        clearEgressReport();
                      }}
                    />
                    <Button type="button" onClick={saveGlobalProxy} disabled={!loaded || savingGlobalProxy}>
                      Save
                    </Button>
                    {/* 测表单里当前的值，不要求先保存。 */}
                    <Button type="button" variant="outline" onClick={testEgress} disabled={!loaded || egressTesting}>
                      {egressTesting ? "Testing…" : "Test proxy"}
                    </Button>
                  </div>
                  <p className="text-muted-foreground text-xs">
                    {globalProxyInput.trim() ? "Configured · all target traffic uses this proxy" : "Not configured · target traffic connects directly"}
                  </p>
                  <details className="text-xs">
                    <summary className="text-muted-foreground cursor-pointer select-none">How proxying works</summary>
                    <p className="text-muted-foreground mt-2 max-w-4xl leading-relaxed">
                      With traffic capture enabled, this proxy is used as the upstream for the recording proxy. Otherwise, it is provided to Agent bash and WebFetch requests. HTTP, HTTPS, and SOCKS5 proxies with credentials are supported. When traffic capture is disabled, command-line tools must support ALL_PROXY to use SOCKS5; tools such as Python urllib may not. Enabling traffic capture avoids this limitation.
                    </p>
                  </details>
                </section>
                <details className="text-xs">
                  <summary className="text-muted-foreground cursor-pointer select-none">Reconnect policy</summary>
                  <div className="mt-3 space-y-3 rounded-md border p-3 sm:p-4">
                    <p className="text-muted-foreground">
                      Paused tasks retry the proxy according to this policy. Once attempts are exhausted, the task remains paused until manually resumed.
                    </p>
                    <div className="grid grid-cols-1 items-end gap-3 sm:grid-cols-[minmax(0,1fr)_minmax(0,1fr)_auto]">
                      <div className="space-y-1.5">
                        <Label htmlFor="proxy-reconnect-max">Maximum reconnect attempts</Label>
                        <Input
                          id="proxy-reconnect-max"
                          type="number"
                          min={1}
                          value={proxyReconnectMax}
                          onChange={(e) => setProxyReconnectMax(e.target.value)}
                        />
                      </div>
                      <div className="space-y-1.5">
                        <Label htmlFor="proxy-reconnect-interval">Reconnect interval (seconds)</Label>
                        <Input
                          id="proxy-reconnect-interval"
                          type="number"
                          min={1}
                          value={proxyReconnectInterval}
                          onChange={(e) => setProxyReconnectInterval(e.target.value)}
                        />
                      </div>
                      <Button type="button" variant="outline" className="w-full sm:w-auto" onClick={saveProxyReconnect}>
                        Save policy
                      </Button>
                    </div>
                  </div>
                </details>
                <div className="flex items-center justify-between gap-4 rounded-md border px-3 py-2">
                  <div className="min-w-0">
                    <Label htmlFor="show-egress-ip" className="text-sm font-normal">
                      Show egress IP in the header
                    </Label>
                    <p className="text-muted-foreground text-xs">
                      When enabled, show the direct and global proxy IPs, using the saved global proxy for the probe.
                    </p>
                  </div>
                  <Switch
                    id="show-egress-ip"
                    checked={showEgressIp}
                    disabled={!settingsLoaded || savingEgressVisibility || egressProbeLoading}
                    onCheckedChange={(checked) => void toggleEgressVisibility(checked)}
                  />
                </div>
                {/* 紧贴结果区：用户看两行时正好需要它。细节放文档。 */}
                <details className="text-xs">
                  <summary className="text-muted-foreground cursor-pointer select-none">About egress IP tests</summary>
                  <p className="text-muted-foreground mt-2 leading-relaxed">
                    The test contacts an IP echo service. By default, it uses api.ipify.org and falls back to ifconfig.me.
                    If a custom address is configured, only that service is used. Compare the direct and proxy IPs in the results to verify the proxy. You can test a changed address without saving it first.
                  </p>
                </details>
                {/* 内网里公网回显不可达，两行都会失败——不是代理坏了。 */}
                <details className="text-xs">
                  <summary className="text-muted-foreground cursor-pointer select-none">
                    Custom IP probe service (optional)
                  </summary>
                  <div className="mt-2 flex flex-col gap-2">
                    <Label htmlFor="egress-probe-url">IP probe service URL</Label>
                    <div className="flex flex-col gap-2 sm:flex-row">
                      <Input
                        id="egress-probe-url"
                        autoComplete="off"
                        placeholder="Leave blank to use the default service; for an intranet, enter http://<internal-IP>:<port>/"
                        value={egressProbeUrl}
                        disabled={!settingsLoaded || savingEgressProbeUrl}
                        onChange={(e) => {
                          setEgressProbeUrl(e.target.value);
                          clearEgressReport();
                        }}
                      />
                      <Button
                        type="button"
                        variant="outline"
                        disabled={!settingsLoaded || savingEgressProbeUrl}
                        onClick={() => void saveEgressProbeUrl()}
                      >
                        {savingEgressProbeUrl ? "Saving…" : "Save probe service"}
                      </Button>
                    </div>
                    <span className="text-muted-foreground leading-relaxed">
                      After saving, this service is used for the header egress IP, proxy tests, health checks, and automatic reconnects. It remains active after a page refresh or restart. Clear the URL and save to restore the default service.
                    </span>
                    <span className="text-muted-foreground leading-relaxed">
                      A custom IP probe service must return a plain-text IP or JSON in the format <code>{'{"ip":"1.2.3.4"}'}</code>.
                      If the public service is inaccessible from your intranet, or your machine and intranet proxy share a public egress IP, use an intranet IP probe service. Different IPs for 1 and 2 indicate that traffic is going through the intranet proxy.
                    </span>
                  </div>
                </details>
                {egressReport && (
                  <div className="flex flex-col gap-1 text-xs">
                    {egressReport.checks.map((c) => {
                      // 经 MITM 时 via 是 HTTP（MITM 自己是代理），标出来会让人以为填错了。
                      const viaCapture = !!c.path && c.path !== egressReport.configured;
                      return (
                        <div key={c.key} className="flex flex-wrap items-center gap-x-2">
                          <span
                            className={
                              c.skipped
                                ? "text-muted-foreground"
                                : c.result.ok
                                  ? "text-emerald-600"
                                  : "text-destructive"
                            }
                          >
                            {c.skipped ? "—" : c.result.ok ? "✓" : "✗"}
                          </span>

                          <span
                            className="w-56 shrink-0"
                            title={
                              c.key === "direct"
                                ? "Connect directly without a proxy, for comparison with check 2"
                                : viaCapture
                                  ? `Actual probe URL: ${c.path} (request forwarded through traffic capture)`
                                  : "Probe through this proxy without saving it first"
                            }
                          >
                            {c.label}
                          </span>
                          {c.skipped ? (
                            <span className="text-muted-foreground">{c.skip_reason}</span>
                          ) : c.result.ok ? (
                            <span className="font-mono">
                              <span title="Egress IP as seen by the external service">{c.result.exit_ip}</span> ·{" "}
                              <span title="Full round-trip time from ARTEX sending the request to receiving the response, including the proxy hop when used">
                                {c.result.latency_ms}ms
                              </span>
                              {c.result.via && c.result.via !== "direct" && !viaCapture && (
                                <span className="text-muted-foreground">
                                  {" "}
                                  · via {c.result.via_label ?? c.result.via}
                                </span>
                              )}
                            </span>
                          ) : (
                            // 只显示第一个地址的原因：refused=代理没开，timeout=出不了网。
                            <span className="text-destructive">
                              {c.result.probes?.find((p) => p.error)?.error ?? c.result.error}
                            </span>
                          )}
                        </div>
                      );
                    })}
                    {egressReport.warnings?.map((w) => (
                      <p key={w} className="text-amber-700">
                        ⚠ {w}
                      </p>
                    ))}
                  </div>
                )}
                <section className="space-y-3 border-t pt-4">
                  <div className="flex flex-wrap items-center justify-between gap-2">
                    <div>
                      <p className="text-sm font-medium">
                        Proxy pool <span className="text-muted-foreground font-normal">{proxyPool.length}</span>
                      </p>
                      <p className="text-muted-foreground mt-1 text-xs">
                        Active:
                        {proxyPool.find((entry) => entry.id === proxyPoolActiveId)?.name ??
                          (proxyPoolActiveId ? "Selected proxy" : "Direct")}
                      </p>
                    </div>
                    <Button type="button" variant="outline" size="sm" onClick={testAllProxyPool}>
                      Test all
                    </Button>
                  </div>
                  <details className="rounded-md border px-3 py-2 text-xs">
                    <summary className="text-muted-foreground cursor-pointer select-none">Import proxies in bulk</summary>
                    <Textarea
                      className="mt-3 font-mono"
                      rows={3}
                      placeholder="One proxy per line: URL|name|notes"
                      value={proxyPoolText}
                      onChange={(e) => setProxyPoolText(e.target.value)}
                    />
                    <div className="mt-2 flex flex-wrap items-center gap-2">
                      <Button type="button" size="sm" onClick={importProxyPool} disabled={!proxyPoolText.trim()}>
                        Import
                      </Button>
                      <span className="text-muted-foreground">Proxy credentials are hidden when displaying addresses.</span>
                    </div>
                  </details>
                  <div className="space-y-2">
                    {proxyPool.length === 0 ? (
                      <p className="text-muted-foreground text-xs">No proxies in the pool.</p>
                    ) : (
                      proxyPool.map((entry) => (
                        <div
                          key={entry.id}
                          className="grid gap-3 rounded-md border p-3 sm:grid-cols-[minmax(0,1fr)_auto] sm:items-center"
                        >
                          <div className="flex min-w-0 items-start gap-3">
                            <Switch
                              checked={entry.enabled}
                              onCheckedChange={() => toggleProxyPoolEntry(entry)}
                              aria-label="Enable proxy"
                            />
                            <div className="grid min-w-0 flex-1 gap-x-5 gap-y-2 text-xs sm:grid-cols-2">
                              <div className="min-w-0">
                                <p className="text-muted-foreground">Proxy address</p>
                                <p className="truncate font-mono" title={entry.url}>
                                  {entry.name ? `${entry.name} · ` : ""}
                                  {entry.url}
                                </p>
                              </div>
                              <div>
                                <p className="text-muted-foreground">Test egress IP</p>
                                <p className="break-all font-mono">
                                  {entry.egress_ip || (entry.last_error ? "Probe failed" : "Not tested")}
                                </p>
                              </div>
                              <div className="flex flex-wrap items-center gap-x-2 gap-y-1">
                                <span
                                  className={
                                    entry.health === "healthy"
                                      ? "text-emerald-600"
                                      : entry.health === "disabled"
                                        ? "text-muted-foreground"
                                        : "text-amber-600"
                                  }
                                >
                                  {!entry.enabled
                                    ? "Disabled"
                                    : entry.health === "healthy"
                                      ? "Healthy"
                                      : entry.health === "unhealthy-cooling"
                                        ? "Cooling down"
                                        : "Pending"}
                                </span>
                                {entry.id === proxyPoolActiveId && <span className="text-primary">In use</span>}
                                <span className="text-muted-foreground">{entry.consecutive_failures} failures</span>
                              </div>
                              <div className="text-muted-foreground">
                                {entry.latency_ms != null ? `${entry.latency_ms} ms` : "No latency data"}
                                {entry.last_checked_at &&
                                  !entry.last_checked_at.startsWith("0001-") &&
                                  ` · ${new Date(entry.last_checked_at).toLocaleTimeString()}`}
                                {entry.last_error && <span className="text-destructive"> · {entry.last_error}</span>}
                              </div>
                            </div>
                          </div>
                          <div className="flex justify-end gap-1">
                            <Button type="button" variant="ghost" size="sm" onClick={() => testOneProxyPool(entry.id)}>
                              Test
                            </Button>
                            <Button
                              type="button"
                              variant="ghost"
                              size="sm"
                              onClick={() => deleteProxyPoolEntry(entry.id)}
                            >
                              Delete
                            </Button>
                          </div>
                        </div>
                      ))
                    )}
                  </div>
                  <details className="rounded-md border px-3 py-2 text-xs">
                    <summary className="text-muted-foreground cursor-pointer select-none">Rotation and health-check policy</summary>
                    <div className="mt-3 space-y-3">
                      <p className="text-muted-foreground">Configure when to switch proxies and how often to run health checks.</p>
                      <div className="flex flex-wrap items-center gap-x-8 gap-y-3 border-b pb-3">
                        <div className="flex items-center gap-3">
                          <Label htmlFor="proxy-pool-timed">Timed rotation</Label>
                          <Switch id="proxy-pool-timed" checked={proxyPoolTimed} onCheckedChange={setProxyPoolTimed} />
                        </div>
                        <div className="flex items-center gap-3">
                          <Label htmlFor="proxy-pool-failure">Rotate on failure</Label>
                          <Switch
                            id="proxy-pool-failure"
                            checked={proxyPoolFailure}
                            onCheckedChange={setProxyPoolFailure}
                          />
                        </div>
                      </div>
                      <div className="grid max-w-2xl grid-cols-1 gap-x-5 gap-y-4 sm:grid-cols-2">
                        <div className="space-y-1.5">
                          <Label htmlFor="proxy-pool-strategy">Rotation order</Label>
                          <Select
                            value={proxyPoolStrategy}
                            onValueChange={(v) => setProxyPoolStrategy(v as "sequential" | "random")}
                          >
                            <SelectTrigger id="proxy-pool-strategy" className="w-full">
                              <SelectValue />
                            </SelectTrigger>
                            <SelectContent>
                              <SelectItem value="sequential">Sequential</SelectItem>
                              <SelectItem value="random">Random</SelectItem>
                            </SelectContent>
                          </Select>
                        </div>
                        <div className="space-y-1.5">
                          <Label htmlFor="proxy-pool-interval">Rotation interval (seconds, ≥3)</Label>
                          <Input
                            id="proxy-pool-interval"
                            type="number"
                            min={3}
                            value={proxyPoolInterval}
                            onChange={(e) => setProxyPoolInterval(e.target.value)}
                          />
                        </div>
                        <div className="space-y-1.5">
                          <Label htmlFor="proxy-pool-threshold">Consecutive failure threshold</Label>
                          <Input
                            id="proxy-pool-threshold"
                            type="number"
                            min={1}
                            value={proxyPoolThreshold}
                            onChange={(e) => setProxyPoolThreshold(e.target.value)}
                          />
                        </div>
                        <div className="space-y-1.5">
                          <Label htmlFor="proxy-pool-cooldown">Failure cooldown (seconds)</Label>
                          <Input
                            id="proxy-pool-cooldown"
                            type="number"
                            min={1}
                            value={proxyPoolCooldown}
                            onChange={(e) => setProxyPoolCooldown(e.target.value)}
                          />
                        </div>
                        <div className="space-y-1.5">
                          <Label htmlFor="proxy-pool-health-interval">Health-check interval (seconds)</Label>
                          <Input
                            id="proxy-pool-health-interval"
                            type="number"
                            min={1}
                            value={proxyPoolHealthInterval}
                            onChange={(e) => setProxyPoolHealthInterval(e.target.value)}
                          />
                        </div>
                      </div>
                      <div className="flex justify-end border-t pt-3">
                        <Button
                          type="button"
                          variant="outline"
                          className="w-full sm:w-auto"
                          onClick={saveProxyPoolStrategy}
                        >
                          Save rotation policy
                        </Button>
                      </div>
                    </div>
                  </details>
                  {proxyPoolLogs.length > 0 && (
                    <details className="text-xs">
                      <summary className="text-muted-foreground cursor-pointer select-none">
                        Recent switches ({proxyPoolLogs.length})
                      </summary>
                      <div className="mt-2 space-y-1">
                        {proxyPoolLogs
                          .slice(-5)
                          .reverse()
                          .map((log) => (
                            <p key={`${log.at}-${log.to}`} className="text-muted-foreground">
                              {new Date(log.at).toLocaleTimeString()} · {log.from || "Direct"} → {log.to || "Direct"} ·{" "}
                              {log.reason}
                            </p>
                          ))}
                      </div>
                    </details>
                  )}
                </section>
              </CardContent>
            </Card>

            <Card
              className={`${isSettingVisible("agent", "operational constraints allow deny planner worker brute force") ? "" : "hidden"} mb-4 break-inside-avoid md:mb-6`}
            >
              <CardHeader>
                <CardTitle className="flex items-center gap-2 text-base">
                  <ShieldAlertIcon className="size-4" />
                  Inject operational constraints
                </CardTitle>
                <CardDescription>
                  When enabled, each task's <b>operational constraints</b> (allow/deny rules managed in the task overview) are added to the relevant Agent's system prompt to define exploration boundaries, such as “test only the current port” or “do not brute-force.”
                  <br />
                  Injection can be controlled separately for the <b>planner</b> and <b>worker</b>. Both are enabled by default. Changes take effect on the next turn without rebuilding Agents. Disabled Agents will no longer receive these constraints.
                </CardDescription>
              </CardHeader>
              <CardContent className="flex flex-col gap-4">
                <div className="flex items-center justify-between gap-4">
                  <Label htmlFor="inject-planner" className="text-sm font-normal text-muted-foreground">
                    Inject into planner{injectPlanner ? " · Enabled" : " · Disabled"}
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
                    Inject into worker{injectWorker ? " · Enabled" : " · Disabled"}
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

            <Card
              className={`${isSettingVisible("agent", "experimental noa compaction") ? "" : "hidden"} mb-4 break-inside-avoid md:mb-6`}
            >
              <CardHeader>
                <CardTitle className="flex items-center gap-2 text-base">
                  <FlaskConicalIcon className="size-4" />
                  Experimental features
                </CardTitle>
                <CardDescription>
                  These features are still being evaluated and are disabled by default. They may change Agent behavior or stability; understand the impact before enabling them.
                  <br />
                  <b>noa context compaction</b>: the model actively compacts long conversation histories (norma v0.4.0). When enabled, the four integrated Agent types (<b>planner / worker / main Agent / chat</b>) use noa instead of built-in compaction. Original compacted content is archived in the task working directory for review. Changes apply to future runs without rebuilding Agents; disabling restores built-in compaction immediately.
                </CardDescription>
              </CardHeader>
              <CardContent className="flex items-center justify-between gap-4">
                <Label htmlFor="noa-compaction" className="text-sm font-normal text-muted-foreground">
                  noa context compaction{noaCompaction ? " · Enabled" : " · Disabled"}
                </Label>
                <Switch
                  id="noa-compaction"
                  checked={noaCompaction}
                  disabled={!loaded}
                  onCheckedChange={toggleNoaCompaction}
                />
              </CardContent>
            </Card>

            <Card
              className={`${isSettingVisible("agent", "automatic titles tasks conversations") ? "" : "hidden"} mb-4 break-inside-avoid md:mb-6`}
            >
              <CardHeader>
                <CardTitle>Automatic titles</CardTitle>
                <CardDescription>
                  Generate a short title from the first message of Agent-created tasks and conversations. These controls are independent and disabled by default.
                </CardDescription>
              </CardHeader>
              <CardContent>
                <Field orientation="horizontal">
                  <FieldContent>
                    <FieldLabel htmlFor="task-auto-title">Task title</FieldLabel>
                    <FieldDescription>Automatically generate a title when an Agent creates a task. If generation fails, the task remains untitled.</FieldDescription>
                  </FieldContent>
                  <Switch
                    id="task-auto-title"
                    checked={taskAutoTitleEnabled}
                    disabled={!loaded}
                    onCheckedChange={(value) => toggleAutoTitle("task_auto_title_enabled", value)}
                  />
                </Field>
                <Field orientation="horizontal" className="mt-5">
                  <FieldContent>
                    <FieldLabel htmlFor="chat-auto-title">Conversation title</FieldLabel>
                    <FieldDescription>
                      Automatically generate a conversation title after the first message. The truncated message is shown first and kept if generation fails.
                    </FieldDescription>
                  </FieldContent>
                  <Switch
                    id="chat-auto-title"
                    checked={chatAutoTitleEnabled}
                    disabled={!loaded}
                    onCheckedChange={(value) => toggleAutoTitle("chat_auto_title_enabled", value)}
                  />
                </Field>
              </CardContent>
            </Card>

            <Card
              className={`${isSettingVisible("agent", "task intelligence credentials leads") ? "" : "hidden"} mb-4 break-inside-avoid md:mb-6`}
            >
              <CardHeader>
                <CardTitle>Task intelligence</CardTitle>
                <CardDescription>Automatically record credentials, important leads, and sensitive data found during a task, preserving their source evidence.</CardDescription>
              </CardHeader>
              <CardContent>
                <Field orientation="horizontal">
                  <FieldContent>
                    <FieldLabel htmlFor="task-intelligence">Automatic Agent recording</FieldLabel>
                    <FieldDescription>
                      Disabled by default. Reporting credentials, important leads, and sensitive data during task execution adds tokens to Agent prompts and reports. This setting only records intelligence; it does not start credential processing. Disabling it also disables the credential Agent and traffic linking below. Existing intelligence and processed results remain available.
                    </FieldDescription>
                  </FieldContent>
                  <Switch
                    id="task-intelligence"
                    checked={intelligenceEnabled}
                    disabled={!loaded || savingIntelligence || savingCredentials}
                    onCheckedChange={async (value) => {
                      setSavingIntelligence(true);
                      try {
                        const settings = await api.setSettings({ task_intelligence_enabled: value });
                        setIntelligenceEnabled(!!settings.task_intelligence_enabled);
                        setCredentialAgent(!!settings.credential_agent_enabled);
                        setCredentialBinding(!!settings.credential_traffic_binding);
                        toast.success(value ? "Automatic task intelligence recording enabled." : "Automatic task intelligence recording disabled.");
                      } catch (error) {
                        toast.error("Save failed: " + (error as Error).message);
                      } finally {
                        setSavingIntelligence(false);
                      }
                    }}
                  />
                </Field>
                <Field orientation="horizontal" className="mt-5">
                  <FieldContent>
                    <FieldLabel htmlFor="credential-agent">Credential processing Agent</FieldLabel>
                    <FieldDescription>
                      Disabled by default. Separately processes existing and new credentials, identifying and highlighting usernames, passwords, tokens, and cookies without changing original reports or verification status. Complex text uses additional model tokens; parseable JSON does not require a model. Enable automatic recording above first. Configure the model under Agent → Credential processing.
                    </FieldDescription>
                  </FieldContent>
                  <Switch
                    id="credential-agent"
                    checked={credentialAgent}
                    disabled={!loaded || savingCredentials || savingIntelligence || !intelligenceEnabled}
                    onCheckedChange={(value) => void saveCredentialOptions({ credential_agent_enabled: value })}
                  />
                </Field>
                {credentialAgent ? (
                  <Field orientation="horizontal" className="mt-5">
                    <FieldContent>
                      <FieldLabel htmlFor="credential-traffic">Link traffic</FieldLabel>
                      <FieldDescription>
                        Disabled by default and available only when the Agent is enabled. Match assets and credentials against captured traffic, link real requests or responses, and save matching excerpts. It will not log in or treat matching traffic as successful verification. Disabling the Agent also disables this option, while existing links are retained.
                      </FieldDescription>
                    </FieldContent>
                    <Switch
                      id="credential-traffic"
                      checked={credentialBinding}
                      disabled={!loaded || savingCredentials}
                      onCheckedChange={(value) => void saveCredentialOptions({ credential_traffic_binding: value })}
                    />
                  </Field>
                ) : null}
              </CardContent>
            </Card>

            <Card
              className={`${isSettingVisible("network", "web search Brave Tavily DuckDuckGo DeepSeek web_search") ? "" : "hidden"} mb-4 break-inside-avoid md:mb-6`}
            >
              <CardHeader>
                <CardTitle className="flex items-center gap-2 text-base">
                  <SearchIcon className="size-4" />
                  Web search
                </CardTitle>
                <CardDescription>
                  This is the <b>global switch and source configuration</b> for web search. When enabled, each <b>Agent profile</b> can independently enable <b>web_search</b> (which returns titles, links, and summaries only; WebFetch retrieves page content). Web search does <b>not</b> use the traffic-recording proxy and is independent of traffic capture.
                  <br />
                  Sources: <b>DuckDuckGo (ddgs)</b> (no API key required), <b>Brave (free tier)</b> (requires a Brave API key), <b>Tavily</b> (requires a Tavily API key), or <b>DeepSeek</b> (requires a separate Search API key). Agent web search controls are unavailable when the global switch is off.
                </CardDescription>
              </CardHeader>
              <CardContent className="flex flex-col gap-4">
                <div className="flex items-center justify-between gap-4">
                  <Label htmlFor="web-search" className="text-sm font-normal text-muted-foreground">
                    {webSearch ? "Enabled · can be configured per Agent" : "Disabled · Agents cannot use web search"}
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
                        <SelectValue placeholder="Select a source" />
                      </SelectTrigger>
                      <SelectContent>
                        <SelectItem value="ddgs">DuckDuckGo (ddgs · free, no key)</SelectItem>
                        <SelectItem value="brave-free">Brave (free tier · key required)</SelectItem>
                        <SelectItem value="tavily">Tavily (key required)</SelectItem>
                        <SelectItem value="deepseek">DeepSeek (official)</SelectItem>
                      </SelectContent>
                    </Select>
                  </div>
                )}

                {webSearch && backend === "deepseek" && (
                  <div className="border-border/60 bg-muted/30 flex flex-col gap-2 rounded-md border p-3">
                    <p className="text-sm font-medium">DeepSeek web search</p>
                    <p className="text-muted-foreground text-xs leading-relaxed">
                      Uses DeepSeek's separate Search and Fetch APIs for search and page extraction. The selected Agent model generates queries and summarizes results.
                    </p>
                    <p className="text-muted-foreground text-xs leading-relaxed">
                      Search requests are sent from the ARTEX server to DeepSeek and can use the separate egress proxy above. The API key is stored in server settings and is never displayed.
                    </p>
                    <div className="flex items-center gap-2">
                      <Input
                        type="password"
                        autoComplete="off"
                        placeholder={deepSeekKeySet ? "Configured (leave blank to keep current key)" : "DeepSeek Search API key"}
                        value={deepSeekKeyInput}
                        disabled={!loaded || savingDeepSeekKey}
                        onChange={(e) => setDeepSeekKeyInput(e.target.value)}
                      />
                      <Button
                        type="button"
                        onClick={saveDeepSeekKey}
                        disabled={!loaded || savingDeepSeekKey || deepSeekKeyInput.trim() === ""}
                      >
                        Save
                      </Button>
                    </div>
                    {deepSeekKeySet && <span className="text-xs text-emerald-500">API key configured</span>}
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
                        placeholder={braveKeySet ? "Configured (leave blank to keep current key)" : "Enter Brave API key"}
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
                        Brave is selected but no API key is configured. Web search will remain disabled until a key is saved.
                      </p>
                    )}
                    <p className="text-muted-foreground text-xs">
                      The free tier includes about 2,000 queries per month. Get an API key at https://brave.com/search/api/.
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
                        placeholder={tavilyKeySet ? "Configured (leave blank to keep current key)" : "Enter Tavily API key (tvly-…)"}
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
                        Tavily is selected but no API key is configured. Web search will remain disabled until a key is saved.
                      </p>
                    )}
                    <p className="text-muted-foreground text-xs">Register at https://tavily.com to get an API key.</p>
                  </div>
                )}

                {webSearch && (
                  <div className="flex flex-col gap-2">
                    <Label htmlFor="ws-proxy" className="text-sm font-normal text-muted-foreground">
                      Egress proxy (optional)
                    </Label>
                    <div className="flex items-center gap-2">
                      <Input
                        id="ws-proxy"
                        autoComplete="off"
                        placeholder="http://host:port or socks5://host:port (leave blank for direct connection)"
                        value={proxyInput}
                        disabled={!loaded || savingProxy}
                        onChange={(e) => setProxyInput(e.target.value)}
                      />
                      <Button type="button" onClick={saveProxy} disabled={!loaded || savingProxy}>
                        Save
                      </Button>
                    </div>
                    <p className="text-muted-foreground text-xs">
                      A separate egress proxy used only to access search endpoints (VPN/SOCKS, etc.). It is independent of the traffic-recording MITM proxy and is used when direct network access is unavailable.
                    </p>
                  </div>
                )}

                {webSearch && (
                  <div className="flex items-center justify-between gap-4 border-t pt-4">
                    <p className="text-muted-foreground text-xs">
                      Run a search for “test” with the current source, proxy, and API key to verify the configuration.
                    </p>
                    <Button
                      type="button"
                      variant="outline"
                      onClick={runTest}
                      disabled={!loaded || testing}
                      className="shrink-0"
                    >
                      {testing ? "Testing…" : "Test search"}
                    </Button>
                  </div>
                )}
              </CardContent>
            </Card>

            <Card
              className={`${isSettingVisible("network", "Python interpreter custom script") ? "" : "hidden"} mb-4 break-inside-avoid md:mb-6`}
            >
              <CardHeader>
                <CardTitle className="flex items-center gap-2 text-base">
                  <RadioTowerIcon className="size-4" />
                  Custom scripts · Python interpreter
                </CardTitle>
                <CardDescription>
                  Runs Python for custom <b>script</b> tools. The interpreter is detected on startup (python3 is preferred). Enter the absolute path to a virtual environment or a specific version, or leave blank to detect it at runtime.
                </CardDescription>
              </CardHeader>
              <CardContent className="flex flex-col gap-3">
                <div className="flex items-center gap-2">
                  <Input
                    className="font-mono text-sm"
                    placeholder="/usr/bin/python3 (leave blank for automatic detection)"
                    value={pyInterp}
                    disabled={!loaded || saving}
                    onChange={(e) => setPyInterp(e.target.value)}
                  />
                  <Button variant="outline" onClick={detectPython} disabled={!loaded || saving}>
                    Detect again
                  </Button>
                  <Button onClick={savePython} disabled={!loaded || saving}>
                    Save
                  </Button>
                </div>
              </CardContent>
            </Card>

            <Card
              className={`${isSettingVisible("network", "Worker concurrency model pool") ? "" : "hidden"} mb-4 break-inside-avoid md:mb-6`}
            >
              <CardHeader>
                <CardTitle className="flex items-center gap-2 text-base">
                  <CpuIcon className="size-4" />
                  Worker concurrency
                </CardTitle>
                <CardDescription>
                  Number of worker Agents to run concurrently per task (default: 3). Higher values allow more parallel probes and use more resources. Changes <b>apply to newly started tasks</b> and do not affect running tasks.
                </CardDescription>
              </CardHeader>
              <CardContent className="flex flex-col gap-3">
                <div className="flex flex-col gap-2 rounded-md border p-3">
                  <div className="flex items-center justify-between">
                    <Label className="text-sm">Multi-model Worker pool</Label>
                    <Button
                      variant="outline"
                      size="sm"
                      onClick={addWorkerPool}
                      disabled={!loaded || !settingsLoaded || savingWorkers}
                    >
                      Add model pool
                    </Button>
                  </div>
                  {workerPools.map((pool, index) => {
                    const selected = llmProfiles.find((profile) => profile.id === pool.profile_id);
                    const placeholder =
                      selected?.name || (pool.profile_id ? `Profile #${pool.profile_id} was deleted or is unavailable` : "Select an LLM profile");
                    return (
                      // biome-ignore lint/suspicious/noArrayIndexKey: pool rows are edited in place and only deleted.
                      <div className="flex items-center gap-2" key={`${pool.profile_id}-${index}`}>
                        <Select
                          value={pool.profile_id || undefined}
                          disabled={!settingsLoaded || savingWorkers}
                          onValueChange={(value) =>
                            setWorkerPools((current) =>
                              current.map((item, itemIndex) =>
                                itemIndex === index ? { ...item, profile_id: value } : item,
                              ),
                            )
                          }
                        >
                          <SelectTrigger className="min-w-0 flex-1">
                            <SelectValue placeholder={placeholder} />
                          </SelectTrigger>
                          <SelectContent>
                            {!selected && pool.profile_id ? (
                              <SelectItem value={pool.profile_id}>Profile #{pool.profile_id} was deleted or is unavailable</SelectItem>
                            ) : null}
                            {llmProfiles
                              .filter((profile) => profile.format !== "jev")
                              .map((profile) => (
                                <SelectItem key={profile.id} value={profile.id}>
                                  {profile.name} · {profile.model}
                                </SelectItem>
                              ))}
                          </SelectContent>
                        </Select>
                        <Input
                          type="number"
                          min={1}
                          className="w-24 font-mono text-sm"
                          value={pool.workers}
                          disabled={!settingsLoaded || savingWorkers}
                          onChange={(event) =>
                            setWorkerPools((current) =>
                              current.map((item, itemIndex) =>
                                itemIndex === index ? { ...item, workers: Number(event.target.value) } : item,
                              ),
                            )
                          }
                        />
                        <Button
                          variant="ghost"
                          size="sm"
                          disabled={!settingsLoaded || savingWorkers}
                          onClick={() =>
                            setWorkerPools((current) => current.filter((_, itemIndex) => itemIndex !== index))
                          }
                        >
                          Remove
                        </Button>
                      </div>
                    );
                  })}
                  {workerPools.length > 0 ? (
                    <div className="text-muted-foreground text-sm">
                      Total: {workerPools.reduce((sum, pool) => sum + (Number(pool.workers) || 0), 0)} Worker sessions
                    </div>
                  ) : null}
                </div>
                <div className="flex items-center gap-2">
                  <Input
                    type="number"
                    min={1}
                    className="w-32 font-mono text-sm"
                    placeholder="3"
                    value={workers}
                    disabled={!loaded || !settingsLoaded || savingWorkers || workerPools.length > 0}
                    onChange={(e) => setWorkers(e.target.value)}
                  />
                  {workerPools.length > 0 ? (
                    <span className="text-muted-foreground text-xs">Concurrency is determined by the configured pool sizes.</span>
                  ) : null}
                  <Button onClick={saveWorkers} disabled={!loaded || !settingsLoaded || savingWorkers}>
                    Save
                  </Button>
                </div>
                {!settingsLoaded && loaded ? (
                  <p className="text-destructive text-xs">
                    Failed to load settings. Worker configuration is locked until a refresh succeeds.
                  </p>
                ) : null}
              </CardContent>
            </Card>

            <Card
              className={`${isSettingVisible("chat", "conversation input send key Enter Shift") ? "" : "hidden"} mb-4 break-inside-avoid md:mb-6`}
            >
              <CardHeader>
                <CardTitle className="flex items-center gap-2 text-base">
                  <KeyboardIcon className="size-4" />
                  Conversation send key
                </CardTitle>
                <CardDescription>
                  This setting is shared by the chat page and the main Agent input in task details. Changes take effect immediately; no save is required.
                  <br />
                  This preference is stored <b>only in this browser</b> and is not synced to your account. Set it again if you change browsers or clear site data.
                </CardDescription>
              </CardHeader>
              <CardContent className="flex items-center justify-between gap-4">
                <Label htmlFor="chat-send-mode" className="text-sm font-normal text-muted-foreground">
                  Send behavior
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
        </main>
      </div>
    </div>
  );
}
