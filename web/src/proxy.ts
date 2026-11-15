import type { NextRequest } from "next/server";
import { NextResponse } from "next/server";
import i18n from "@/lib/i18n";

const AUTH_PAGES = ["/login", "/setup"];

export async function proxy(request: NextRequest) {
  // Mock demo: no real login; allow access to all pages (the client-side auth guard also allows access).
  if (process.env.NEXT_PUBLIC_MOCK === "1") return NextResponse.next();

  // Pages served by Next dev must pass the same gate as the embedded UI.
  // Only send credentials to a loopback backend; never follow redirects.
  // Remote dev backends remain usable when their optional gate is disabled.
  let gate: Response;
  try {
    const backend = new URL(process.env.AUTOPENTEST_API ?? "http://localhost:8787");
    if (!["http:", "https:"].includes(backend.protocol) || backend.username || backend.password) {
      return new NextResponse(i18n.t("proxy.invalid_backend_config"), { status: 503 });
    }
    const local = ["localhost", "127.0.0.1", "[::1]"].includes(backend.hostname);
    gate = await fetch(new URL("/api/basic-auth/check", backend), {
      headers: local
        ? {
            cookie: request.headers.get("cookie") ?? "",
            authorization: request.headers.get("authorization") ?? "",
            "x-forwarded-proto": request.nextUrl.protocol.replace(":", ""),
          }
        : {},
      credentials: "omit",
      cache: "no-store",
      redirect: "error",
      signal: AbortSignal.timeout(5000),
    });
    if (!local && gate.status === 401) {
      return new NextResponse(i18n.t("proxy.remote_basic_auth"), { status: 503 });
    }
  } catch {
    return new NextResponse(i18n.t("proxy.auth_service_unavailable"), { status: 503 });
  }
  if (!gate.ok) {
    const headers = new Headers({ "Cache-Control": "no-store", "Content-Type": "text/plain; charset=utf-8" });
    const challenge = gate.headers.get("www-authenticate");
    if (challenge) headers.set("WWW-Authenticate", challenge);
    return new NextResponse(gate.status === 401 ? i18n.t("proxy.complete_basic_auth") : i18n.t("proxy.auth_service_unavailable"), {
      status: gate.status,
      headers,
    });
  }
  const finish = (response: NextResponse) => {
    const cookie = gate.headers.get("set-cookie");
    if (cookie) response.headers.append("Set-Cookie", cookie);
    response.headers.set("Cache-Control", "no-store");
    return response;
  };

  const { pathname } = request.nextUrl;
  const token = request.cookies.get("artex_token")?.value;
  const isAuthPage = AUTH_PAGES.some((p) => pathname === p || pathname.startsWith(`${p}/`));

  // Not logged in → redirect to the login page
  if (!token && !isAuthPage) {
    return finish(NextResponse.redirect(new URL("/login", request.url)));
  }

  // When logged in, visiting the login/initialization page → redirect to the main interface
  if (token && isAuthPage) {
    return finish(NextResponse.redirect(new URL("/function/tasks", request.url)));
  }

  return finish(NextResponse.next());
}

export const config = {
  // Skip Next.js internal routes, API routes, favicon, and static files under public/ (including images, fonts, etc.)
  matcher: [
    "/((?!_next/static|_next/image|favicon\\.ico|api/|.*\\.(?:png|jpg|jpeg|gif|webp|svg|ico|woff2?|ttf|otf)$).*)",
  ],
};
