import { me } from "../state/me";

export function Login() {
  const v = me.value;
  const issuer = v?.issuer || "your identity provider";

  const signIn = () => {
    const ret = window.location.pathname + window.location.search;
    window.location.href = `/api/v1/auth/login?return=${encodeURIComponent(ret)}`;
  };

  return (
    <div class="login-page">
      <div class="login-card">
        <div class="login-mark" />
        <div class="login-title">prism</div>
        <div class="login-sub">admin console</div>
        <div class="login-desc">
          sign in with <span class="login-issuer">{shortIssuer(issuer)}</span>{" "}
          to continue.
        </div>
        <button class="login-btn" onClick={signIn}>
          sign in
        </button>
      </div>
    </div>
  );
}

function shortIssuer(issuer: string): string {
  try {
    const u = new URL(issuer);
    return u.host;
  } catch {
    return issuer;
  }
}
