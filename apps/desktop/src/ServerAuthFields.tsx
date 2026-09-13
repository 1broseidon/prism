import type { Ref } from "preact";
import type { HttpAuth } from "./types";
import { Segmented } from "./ui";

/** Shared auth controls: detection suggests a choice, and the user can always change it. */
export function ServerAuthFields({ auth, header, secret, onAuth, onHeader, onSecret, editing = false, requireKey = false, keyRef }: {
  auth: HttpAuth;
  header: string;
  secret: string;
  onAuth: (auth: HttpAuth) => void;
  onHeader: (header: string) => void;
  onSecret: (secret: string) => void;
  editing?: boolean;
  requireKey?: boolean;
  keyRef?: Ref<HTMLInputElement>;
}) {
  return <>
    <div class="field">
      <span>Auth</span>
      <Segmented small label="Authentication" value={auth} options={[
        { value: "none", label: "None" },
        { value: "header", label: "API key" },
        { value: "oauth", label: "OAuth" },
      ]} onChange={onAuth} />
      {auth === "oauth" ? <small>Signs in through your browser. Tokens stay in your keyring.</small> : null}
    </div>
    {auth === "header" ? <>
      <label class="field">
        <span>Header</span>
        <input class="input mono" name="header" disabled={editing && !secret.trim()} value={header} onInput={(event) => onHeader(event.currentTarget.value)} placeholder={editing && !secret.trim() ? "unchanged" : "Authorization"} />
        {editing && !secret.trim() ? <small>Enter the key again to change its header.</small> : null}
      </label>
      <label class="field">
        <span>Key</span>
        <input ref={keyRef} class="input mono" name="key" type="password" required={!editing || requireKey} autoComplete="off" value={secret} onInput={(event) => onSecret(event.currentTarget.value)} placeholder={editing && !requireKey ? "unchanged" : "Enter API key"} />
        <small>Sent as Bearer unless you give a prefix. Stored in your keyring.</small>
      </label>
    </> : null}
  </>;
}
