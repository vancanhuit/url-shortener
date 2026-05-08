// SPA entrypoint. Imports the Tailwind CSS first so its @import is
// processed at build time, then mounts the root Svelte component.
import "./app.css";
import { mount } from "svelte";
import App from "./App.svelte";

const target = document.getElementById("app");
if (!target) {
  throw new Error("missing #app mount target in index.html");
}

mount(App, { target });
