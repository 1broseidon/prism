import { render } from "preact";
import "./styles/tokens.css";
import "./styles/app.css";
import { App } from "./app";
import { startAllPolling } from "./state";

// System theme follows prefers-color-scheme.
function applyTheme() {
  document.documentElement.classList.toggle(
    "dark",
    window.matchMedia("(prefers-color-scheme: dark)").matches,
  );
}
applyTheme();
window
  .matchMedia("(prefers-color-scheme: dark)")
  .addEventListener("change", applyTheme);

startAllPolling();

const root = document.getElementById("app");
if (root) render(<App />, root);
