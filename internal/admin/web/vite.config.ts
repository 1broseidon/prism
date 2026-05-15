import { defineConfig } from "vite";
import preact from "@preact/preset-vite";

// Vite config for Prism admin console.
// - Build output goes to dist/ and is embedded into the Go binary via go:embed.
// - Dev server proxies API calls to the running prism admin port (default :9090).
// - server.host is deliberately NOT set, keeping the dev server on localhost only
//   (mitigates CVE-2025-31125 in older vite versions; we're on 8.x but the discipline holds).
export default defineConfig({
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
    proxy: {
      "/agents": "http://localhost:9090",
      "/backends": "http://localhost:9090",
      "/groups": "http://localhost:9090",
      "/defaults": "http://localhost:9090",
      "/events": "http://localhost:9090",
      "/info": "http://localhost:9090",
      "/health": "http://localhost:9090",
      "/metrics": "http://localhost:9090",
      "/oauth": "http://localhost:9090",
    },
  },
});
