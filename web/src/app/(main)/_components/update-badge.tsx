"use client";

import * as React from "react";

import Link from "next/link";

import { ArrowUpCircleIcon } from "lucide-react";

import { api } from "@/lib/api";
import { useTranslation } from "react-i18next";

export function UpdateBadge() {
  const { t } = useTranslation();
  const [latest, setLatest] = React.useState("");

  React.useEffect(() => {
    let alive = true;
    api
      .checkUpdate()
      .then((r) => {
        if (alive && r.has_update && r.latest) setLatest(r.latest.replace(/^v(?=\d)/, ""));
      })
      .catch(() => {
      });
    return () => {
      alive = false;
    };
  }, []);

  if (!latest) return null;

  return (
    <Link
      href="/system/settings"
      title={t("update.new_version", { version: latest })}
      className="inline-flex items-center gap-1.5 rounded-full bg-primary px-2.5 py-1 font-medium text-primary-foreground text-xs transition-opacity hover:opacity-90"
    >
      <ArrowUpCircleIcon className="size-3.5" />
      <span className="hidden sm:inline">{t("update.new_version_short", { version: latest })}</span>
      <span className="sm:hidden">{t("update.new_version")}</span>
    </Link>
  );
}
