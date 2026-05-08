/// <reference types="svelte" />
/// <reference types="vite/client" />

// TypeScript 6 requires explicit declarations for side-effect imports
// of non-code modules. The SPA imports `./app.css` from main.ts so
// Vite + the @tailwindcss/vite plugin pick up the Tailwind input;
// `*.css` resolves to never (no runtime API consumed) since the
// stylesheet is processed at build time, not imported as a value.
declare module "*.css";
