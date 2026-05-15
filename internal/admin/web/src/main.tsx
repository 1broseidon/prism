import { render } from "preact";
import "./styles/tokens.css";
import "./styles/app.css";
import { App } from "./app";
import { startAllPolling } from "./state";

// Midnight Editorial is dark-only by design. No system theme toggle.

startAllPolling();

const root = document.getElementById("app");
if (root) render(<App />, root);
