import { fileURLToPath } from "node:url";

// Static export: `NEXT_EXPORT=1 next build` produces a purely static directory at web/out, which can be dropped directly into
// the nginx web root to run. Do not set this variable for development (next dev); /api proxying and hot reloading remain enabled.
const isExport = process.env.NEXT_EXPORT === "1";
// Vercel demo: the entire site uses mocks, with no backend and no need for /api proxying.
const isMock = process.env.NEXT_PUBLIC_MOCK === "1";

/** @type {import('next').NextConfig} */
const nextConfig = {
  // Prevent lockfiles in parent directories from affecting root directory inference and asset path generation.
  turbopack: { root: fileURLToPath(new URL(".", import.meta.url)) },
  reactCompiler: true,
  // Allow access to dev resources (HMR) from LAN IPs; add or remove entries as needed.
  // During development, allow access to /_next/* and HMR from any IPv4 source (even if the LAN IP changes).
  // Note: For security, Next disallows a bare "*"; use segmented wildcards. "*.*.*.*" matches any IPv4 address.
  allowedDevOrigins: ["*.*.*.*"],
  compiler: {
    removeConsole: process.env.NODE_ENV === "production",
  },
  ...(isExport
    ? {
        // Pure static export: no Node runtime; images are not optimized; each route produces <route>/index.html.
        output: "export",
        images: { unoptimized: true },
        trailingSlash: true,
      }
    : isMock
      ? {
          // Vercel mock demo: no backend and no need for /api proxying.
          images: { unoptimized: true },
        }
      : {
          // Development: proxy /api/* to the Go backend (default :8787; override with AUTOPENTEST_API).
          async rewrites() {
            const backend = process.env.AUTOPENTEST_API ?? "http://localhost:8787";
            return [{ source: "/api/:path*", destination: `${backend}/api/:path*` }];
          },
        }),
};

export default nextConfig;
