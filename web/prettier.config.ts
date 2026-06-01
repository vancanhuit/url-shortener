import type { Config } from "prettier";

export default {
  plugins: ["prettier-plugin-svelte"],
  overrides: [{ files: "*.svelte", options: { parser: "svelte" } }],
  printWidth: 100,
  trailingComma: "es5",
} satisfies Config;
