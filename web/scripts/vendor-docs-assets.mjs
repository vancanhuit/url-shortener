// Copy the static assets the OpenAPI docs viewers (Swagger UI +
// Redoc) need into `public/static/`, where Vite's static-asset
// pipeline will pick them up verbatim and emit them under
// `dist/static/`. The Go server then serves dist/static/* at
// /static/* alongside the SPA's hashed bundles.
//
// Run automatically on every `npm run build` via the `prebuild`
// script; safe to invoke directly during development.
import { copyFileSync, mkdirSync } from "node:fs";
import { dirname, resolve } from "node:path";
import { fileURLToPath } from "node:url";

const __filename = fileURLToPath(import.meta.url);
const __dirname = dirname(__filename);
const webRoot = resolve(__dirname, "..");
const publicStatic = resolve(webRoot, "public/static");

mkdirSync(publicStatic, { recursive: true });

/** @type {[string, string][]} */
const assets = [
  ["swagger-ui-dist/swagger-ui.css", "swagger-ui.css"],
  ["swagger-ui-dist/swagger-ui-bundle.js", "swagger-ui-bundle.js"],
  ["swagger-ui-dist/swagger-ui-standalone-preset.js", "swagger-ui-standalone-preset.js"],
  ["redoc/bundles/redoc.standalone.js", "redoc.standalone.js"],
];

for (const [from, to] of assets) {
  const src = resolve(webRoot, "node_modules", from);
  const dst = resolve(publicStatic, to);
  copyFileSync(src, dst);
  console.log(`vendored ${from} -> public/static/${to}`);
}
