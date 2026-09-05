import { useEffect, useState } from "preact/hooks";
import * as api from "./api";
import { errorMessage } from "./state";
import type { ManualToken } from "./types";
import { Button, CodeBlock, Label, Screen, describeError } from "./ui";

/** The token stays in the provisioning screen's memory until Done or navigation. */
export function ManualTokenDetails({ issued, onDone }: { issued: ManualToken; onDone: () => void }) {
  const [url, setUrl] = useState<string | null>(null);
  useEffect(() => {
    api.getConnectSnippet().then((snippet) => setUrl(snippet.url)).catch((err) => {
      errorMessage.value = describeError(err);
    });
  }, []);

  return (
    <div class="screen pushed">
      <Screen footer={<Button variant="primary" onClick={onDone}>Done</Button>}>
        <p class="lede">Copy this token now. Prism cannot show it again after you leave this screen.</p>
        <section class="section">
          <Label>API key</Label>
          <CodeBlock text={issued.token} copyable />
          <p class="hint">Works until you revoke or replace it. Your agent's tool permissions still apply.</p>
        </section>
        {url ? <section class="section">
          <Label>Connection settings</Label>
          <CodeBlock text={JSON.stringify({ mcpServers: { prism: { url, headers: { Authorization: `Bearer ${issued.token}` } } } }, null, 2)} copyable />
          <p class="hint">For clients that accept custom HTTP headers. If it has a bearer-token field, paste only the token.</p>
        </section> : null}
      </Screen>
    </div>
  );
}
