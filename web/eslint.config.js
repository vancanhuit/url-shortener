import js from "@eslint/js";
import ts from "typescript-eslint";
import svelte from "eslint-plugin-svelte";
import globals from "globals";
import svelteConfig from "./svelte.config.js";

export default ts.config(
  js.configs.recommended,
  ts.configs.recommended,
  svelte.configs.recommended,
  {
    // Source files run in the browser.
    languageOptions: {
      globals: { ...globals.browser },
    },
  },
  {
    // Svelte components: hand the TS parser to eslint-plugin-svelte so
    // it can parse <script lang="ts"> blocks.
    files: ["**/*.svelte"],
    languageOptions: {
      parserOptions: {
        parser: ts.parser,
        svelteConfig,
        extraFileExtensions: [".svelte"],
      },
    },
  },
  {
    // Config files and Node scripts run in Node, not the browser.
    files: ["*.config.{js,ts}", "scripts/**/*.{js,mjs}"],
    languageOptions: {
      globals: { ...globals.node },
    },
  },
  {
    ignores: ["dist/", "node_modules/", "public/static/"],
  }
);
