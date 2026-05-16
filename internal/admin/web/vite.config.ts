import { defineConfig, loadEnv } from "vite";
import preact from "@preact/preset-vite";

// Minimal ambient — vite.config runs in Node, but we don't want a heavyweight
// @types/node dep just for one variable. Keeps dependency surface small.
declare const process: {
  cwd(): string;
  env: Record<string, string | undefined>;
};

// Vite config for Prism admin console.
// - Build output goes to dist/ and is embedded into the Go binary via go:embed.
// - Dev server proxies API calls to the running prism admin port (default :9086).
//   Override with PRISM_ADMIN_URL when running prism on a non-default port.
// - server.host is deliberately NOT set, keeping the dev server on localhost only
//   (mitigates CVE-2025-31125 in older vite versions; we're on 8.x but the discipline holds).
export default defineConfig(({ mode }) => {
  const env = loadEnv(mode, process.cwd(), "");
  const adminURL = env.PRISM_ADMIN_URL || "http://localhost:9086";
  const apiPaths = [
    "/agents",
    "/backends",
    "/groups",
    "/defaults",
    "/events",
    "/info",
    "/health",
    "/metrics",
    "/oauth",
    "/auth",
  ];
  const proxy = Object.fromEntries(apiPaths.map((p) => [p, adminURL]));

  return {
    plugins: [preact()],
    build: {
      outDir: "dist",
      emptyOutDir: true,
      assetsDir: "assets",
      sourcemap: false,
    },
    server: {
      port: 5173,
      strictPort: true,
      proxy,
    },
  };
});
