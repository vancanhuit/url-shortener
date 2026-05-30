// Vite config for the Svelte SPA. The Go binary `//go:embed`s the
// build output (`web/dist/`) and serves it under `/`, so anything
// here that affects the asset URL layout must stay aligned with the
// Go-side static handler.
import { defineConfig } from "vite";
import { svelte } from "@sveltejs/vite-plugin-svelte";
import tailwindcss from "@tailwindcss/vite";

export default defineConfig({
  plugins: [svelte(), tailwindcss()],
  server: {
    // `npm run dev` proxies API and redirect routes to the Go server
    // running on :8080 (started via `just dev` / `just up`). Keeping
    // this list in sync with the routes mounted in internal/server is
    // a small price for getting same-origin fetches in dev.
    proxy: {
      "/api": "http://localhost:8080",
      "/r": "http://localhost:8080",
      "/livez": "http://localhost:8080",
      "/readyz": "http://localhost:8080",
      "/version": "http://localhost:8080",
    },
  },
  build: {
    // Hashed assets land under dist/assets/<name>-<hash>.<ext>; the
    // Go static handler mounts them at /assets/*. Keeping the
    // default here keeps /assets/* meaningful in URLs.
    assetsDir: "assets",
    sourcemap: false,
    target: "es2022",
  },
});
