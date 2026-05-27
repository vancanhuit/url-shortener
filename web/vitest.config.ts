// Vitest config. Extends the Vite config via mergeConfig so both share
// the same plugin chain (Svelte compiler, Tailwind) without duplicating
// declarations. The `test` block is vitest-only and has no effect on
// the production build.
import { defineConfig, mergeConfig } from "vitest/config";
import viteConfig from "./vite.config";

export default mergeConfig(
  viteConfig,
  defineConfig({
    test: {
      environment: "jsdom",
      setupFiles: ["./src/vitest-setup.ts"],
      include: ["src/**/*.test.ts"],
      coverage: {
        provider: "v8",
        include: ["src/**/*.ts"],
        exclude: ["src/main.ts", "src/vite-env.d.ts", "src/vitest-setup.ts"],
      },
    },
  })
);
