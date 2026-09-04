import { render } from "preact";
import { App } from "./app";
import "./tokens.css";
import "./styles.css";

const scheme = new URLSearchParams(location.search).get("scheme");
if (scheme === "light" || scheme === "dark") document.documentElement.style.colorScheme = scheme;

const root = document.getElementById("app");
if (root) {
  render(<App />, root);
}
