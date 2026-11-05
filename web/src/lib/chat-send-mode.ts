"use client";

import * as React from "react";

import { getLocalStorageValue, setLocalStorageValue } from "@/lib/local-storage.client";

// Chat input box send/newline key bindings. Pure frontend preference: only persisted to localStorage,
// not stored in the database, not synced with the account,
// so changing browsers requires reset. See issue #39 -- 0.3.2 changed Ctrl+Enter send to Enter send,
// this brings back the old key binding as an option.
export type ChatSendMode = "enter" | "ctrl-enter";

export const CHAT_SEND_MODE_KEY = "artex_chat_send_mode";
export const DEFAULT_CHAT_SEND_MODE: ChatSendMode = "enter";

export const CHAT_SEND_MODE_OPTIONS: { value: ChatSendMode; label: string }[] = [
  { value: "enter", label: "Enter to send, Shift+Enter to newline" },
  { value: "ctrl-enter", label: "Ctrl+Enter to send, Enter to newline" },
];

function parseMode(raw: string | null): ChatSendMode {
  return raw === "ctrl-enter" || raw === "enter" ? raw : DEFAULT_CHAT_SEND_MODE;
}

// Subscriber set within the same tab. localStorage's storage event only fires in "other" tabs,
// so after changing settings on this page, need to emit to notify the input box on the same page,
// otherwise a refresh is needed for it to take effect.
const listeners = new Set<() => void>();

function subscribe(listener: () => void) {
  listeners.add(listener);
  window.addEventListener("storage", listener);
  return () => {
    listeners.delete(listener);
    window.removeEventListener("storage", listener);
  };
}

// Returns a string literal, Object.is compares by value, won't cause useSyncExternalStore to loop.
function getSnapshot(): ChatSendMode {
  return parseMode(getLocalStorageValue(CHAT_SEND_MODE_KEY));
}

// Server has no localStorage, render default value first, hydrate then getSnapshot corrects it.
function getServerSnapshot(): ChatSendMode {
  return DEFAULT_CHAT_SEND_MODE;
}

export function useChatSendMode(): ChatSendMode {
  return React.useSyncExternalStore(subscribe, getSnapshot, getServerSnapshot);
}

export function setChatSendMode(mode: ChatSendMode) {
  setLocalStorageValue(CHAT_SEND_MODE_KEY, mode);
  for (const listener of listeners) listener();
}

// shouldSubmitOnKey determines whether a key press should submit.
// isComposing / keyCode 229 means Chinese etc. IME is selecting characters, must let through,
// otherwise Enter to select word would accidentally submit.
// enter mode only excludes Shift, consistent with 0.3.2 behavior character by character --
// users who don't change settings keep the same hand feel.
// ctrl-enter mode accepts both Ctrl and Cmd (macOS).
export function shouldSubmitOnKey(e: React.KeyboardEvent, mode: ChatSendMode): boolean {
  if (e.key !== "Enter") return false;
  if (e.nativeEvent.isComposing || e.nativeEvent.keyCode === 229) return false;
  if (mode === "ctrl-enter") return e.ctrlKey || e.metaKey;
  return !e.shiftKey;
}
