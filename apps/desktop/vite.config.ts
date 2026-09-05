import { defineConfig } from "vite";
import preact from "@preact/preset-vite";

const host = process.env.TAURI_DEV_HOST;

export default defineConfig({
  plugins: [preact()],
  clearScreen: false,
  build: {
    // Keep light-dark() and other modern CSS intact: Tauri's webviews (WebKitGTK 2.46+, WebView2, WKWebView on
    // macOS 14.5+) support them natively, and the transpiled polyfill breaks the runtime color-scheme override.
    cssTarget: ["chrome123", "safari17.5", "firefox120"],
    target: ["chrome123", "safari17.5", "firefox120"],
  },
  server: {
    port: 1420,
    strictPort: true,
    host: host || false,
    hmr: host ? { protocol: "ws", host, port: 1421 } : undefined,
    watch: {
      ignored: ["**/src-tauri/**"],
    },
  },
});
