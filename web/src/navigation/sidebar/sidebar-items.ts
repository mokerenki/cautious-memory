import {
  Activity,
  Ban,
  BellRing,
  Bot,
  Brain,
  Bug,
  ClipboardList,
  FolderOpen,
  FolderSync,
  LayoutDashboard,
  type LucideIcon,
  MessageSquare,
  Network,
  Plug,
  Radio,
  ScrollText,
  Settings2,
  ShieldAlert,
  Sparkles,
  Target,
  Terminal,
  Wrench,
} from "lucide-react";
import { useTranslation } from "react-i18next";

export type NavBadge = "new" | "soon";

export interface NavSubItem {
  id: string;
  title: string;
  url: string;
  icon?: LucideIcon;
  badge?: NavBadge;
  disabled?: boolean;
  newTab?: boolean;
}

interface NavItemBase {
  id: string;
  title: string;
  icon?: LucideIcon;
  badge?: NavBadge;
  disabled?: boolean;
  newTab?: boolean;
}

export interface NavMainLinkItem extends NavItemBase {
  url: string;
  subItems?: never;
}

export interface NavMainParentItem extends NavItemBase {
  subItems: NavSubItem[];
}

export type NavMainItem = NavMainLinkItem | NavMainParentItem;

export interface NavGroup {
  id: number;
  label?: string;
  items: NavMainItem[];
}

export function useSidebarItems(): NavGroup[] {
  const { t } = useTranslation();

  return [
    {
      id: 1,
      label: t("nav.functions"),
      items: [
        { id: "dashboard", title: t("nav.dashboard"), url: "/dashboard", icon: LayoutDashboard },
        { id: "chat", title: t("nav.chat"), url: "/chat", icon: MessageSquare },
        { id: "tasks", title: t("nav.tasks"), url: "/function/tasks", icon: Target },
        { id: "findings", title: t("nav.findings"), url: "/function/findings", icon: Bug },
        { id: "traffic", title: t("nav.traffic"), url: "/function/traffic", icon: Activity },
        { id: "commands", title: t("nav.toolExecution"), url: "/function/commands", icon: Terminal },
        { id: "llm-records", title: t("nav.llmRecording"), url: "/function/llm-records", icon: Radio },
        { id: "assets", title: t("nav.assets"), url: "/function/assets", icon: Network },
        { id: "sync", title: t("nav.assetSync"), url: "/function/sync", icon: FolderSync },
        { id: "workspace", title: t("nav.workspace"), url: "/function/workspace", icon: FolderOpen },
      ],
    },
    {
      id: 2,
      label: t("nav.system"),
      items: [
        { id: "llm", title: t("nav.llm"), url: "/system/llm", icon: Brain },
        { id: "agents", title: t("nav.agent"), url: "/system/agents", icon: Bot },
        { id: "mcp", title: t("nav.mcp"), url: "/system/mcp", icon: Plug },
        { id: "skills", title: t("nav.skill"), url: "/system/skills", icon: Sparkles },
        { id: "tools", title: t("nav.tools"), url: "/system/tools", icon: Wrench },
        { id: "notify", title: t("nav.notifications"), url: "/system/notify", icon: BellRing },
        { id: "intercept", title: t("nav.interceptionRules"), url: "/system/intercept", icon: ShieldAlert },
        { id: "asset-intercept", title: t("nav.assetInterception"), url: "/system/intercept/assets", icon: Ban },
        { id: "approvals", title: t("nav.approvalRecords"), url: "/system/intercept/approvals", icon: ClipboardList },
        { id: "logs", title: t("nav.logs"), url: "/system/logs", icon: ScrollText },
        { id: "settings", title: t("nav.systemSettings"), url: "/system/settings", icon: Settings2 },
      ],
    },
  ];
}
