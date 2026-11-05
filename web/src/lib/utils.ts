import { type ClassValue, clsx } from "clsx";
import { twMerge } from "tailwind-merge";

export function cn(...inputs: ClassValue[]) {
  return twMerge(clsx(inputs));
}

// Helper for Sheet/Dialog onInteractOutside close determination.
//
// Background: Radix overlays (Select dropdown, DropdownMenu, Popover, etc.) inside the drawer portal
// to outside the drawer. Clicking the overlay/drawer outside when an overlay is open wants to close it;
// this pointerdown will be handled by both Select and Sheet DismissableLayer at the same time; Select
// closes first and is a discrete event, React sync flushes, so by the time Sheet's handler runs, the
// overlay's data-state has already flipped to closed -- detecting whether the overlay is open "at the
// moment" is inherently unreliable (verified by testing).
//
// Correct approach: Radix's pointerdown listener is in the bubble phase; we capture in the capture
// phase (earlier than it) and first record "whether an overlay is open right now", then
// onInteractOutside reads this recorded value to decide whether to allow closing.
function isRadixOverlayOpenNow(): boolean {
  if (typeof document === "undefined") return false;
  return !!document.querySelector(
    [
      "[data-slot='select-trigger'][data-state='open']",
      "[data-slot='select-content'][data-state='open']",
      "[role='listbox'][data-state='open']",
      "[data-radix-popper-content-wrapper]",
      "[aria-expanded='true'][data-state='open']",
    ].join(","),
  );
}

let overlayOpenAtLastPointerDown = false;
if (typeof document !== "undefined") {
  document.addEventListener(
    "pointerdown",
    () => {
      overlayOpenAtLastPointerDown = isRadixOverlayOpenNow();
    },
    true, // capture: record before Radix bubble phase pointerdown handler
  );
}

// radixOverlayWasOpenAtPointerDown returns "whether there was a Radix overlay open at the most recent
// pointerdown". The drawer/dialog uses this: when an overlay is open, clicking overlay -> only close
// the overlay, not itself.
export function radixOverlayWasOpenAtPointerDown(): boolean {
  return overlayOpenAtLastPointerDown;
}

// copyText writes text to clipboard, returns whether successful.
// Background: navigator.clipboard is only available in secure contexts (HTTPS / localhost);
// when accessed via IP + HTTP it is undefined, falling back to execCommand("copy") at that point.
export async function copyText(text: string): Promise<boolean> {
  if (navigator.clipboard && window.isSecureContext) {
    try {
      await navigator.clipboard.writeText(text);
      return true;
    } catch {
      // Continue with fallback approach
    }
  }
  try {
    const textarea = document.createElement("textarea");
    textarea.value = text;
    textarea.style.position = "fixed";
    textarea.style.left = "-9999px";
    textarea.style.top = "0";
    document.body.appendChild(textarea);
    textarea.focus();
    textarea.select();
    const ok = document.execCommand("copy");
    document.body.removeChild(textarea);
    return ok;
  } catch {
    return false;
  }
}

export const getInitials = (str: string): string => {
  if (typeof str !== "string" || !str.trim()) return "?";

  return (
    str
      .trim()
      .split(/\s+/)
      .filter(Boolean)
      .map((word) => word[0])
      .join("")
      .toUpperCase() || "?"
  );
};

export function formatCurrency(
  amount: number,
  opts?: {
    currency?: string;
    locale?: string;
    minimumFractionDigits?: number;
    maximumFractionDigits?: number;
    noDecimals?: boolean;
  },
) {
  const { currency = "USD", locale = "en-US", minimumFractionDigits, maximumFractionDigits, noDecimals } = opts ?? {};

  const formatOptions: Intl.NumberFormatOptions = {
    style: "currency",
    currency,
    minimumFractionDigits: noDecimals ? 0 : minimumFractionDigits,
    maximumFractionDigits: noDecimals ? 0 : maximumFractionDigits,
  };

  return new Intl.NumberFormat(locale, formatOptions).format(amount);
}
