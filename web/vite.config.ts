import react from "@vitejs/plugin-react";
import { defineConfig } from "vitest/config";

// The build lands where internal/webui embeds it with the webui tag
// (docs/specs/web.md 3). The module preload polyfill is off: it would be an
// inline script, and the server's CSP allows scripts from files only.
export default defineConfig({
  plugins: [react()],
  build: {
    outDir: "../internal/webui/dist",
    emptyOutDir: true,
    modulePreload: { polyfill: false },
    assetsInlineLimit: 0,
    sourcemap: false,
  },
  test: {
    environment: "happy-dom",
    globals: true,
    include: ["src/**/*.test.{ts,tsx}"],
  },
});
