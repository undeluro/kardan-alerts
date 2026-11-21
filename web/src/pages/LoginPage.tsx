import { KeyRound, Radio } from "lucide-react";
import { useState } from "react";
import { api, type DemoSession } from "../api";
import { Link } from "../router";
import { Notice, Select, inputClass } from "../ui";

export function LoginPage({ onSignIn }: { onSignIn: (session: DemoSession) => void }) {
  const [account, setAccount] = useState("sam");
  const [token, setToken] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");

  const signIn = async () => {
    if (!token.trim() || busy) return;
    setBusy(true);
    setError("");
    try {
      onSignIn(await api.signIn(account, token.trim()));
    } catch (err) {
      setError((err as Error).message);
    } finally {
      setBusy(false);
    }
  };

  return (
    <main className="mx-auto flex min-h-dvh max-w-lg flex-col justify-center gap-5 px-5 py-10">
      <Link to="/landing" className="console-brand w-fit">
        <span className="console-mark"><Radio size={17} aria-hidden="true" /></span>Kardan
      </Link>
      <section className="console-panel p-5 sm:p-7" aria-labelledby="login-title">
        <h1 id="login-title" className="text-2xl font-semibold tracking-tight">Sign in to Kardan</h1>
        <p className="console-muted mt-1.5 text-sm">Use your demo SSO account and access token.</p>
        <form className="mt-5 grid gap-4" onSubmit={(event) => { event.preventDefault(); void signIn(); }}>
          <div className="grid gap-1.5">
            <label htmlFor="demo-account" className="text-sm font-medium">Account</label>
            <Select id="demo-account" value={account} onChange={(event) => {
              setAccount(event.target.value);
              setToken("");
              setError("");
            }} disabled={busy}>
              <option value="sam">Sam Okafor · Operator</option>
              <option value="maria">Maria Rossi · Administrator</option>
            </Select>
          </div>
          <div className="grid gap-1.5">
            <label htmlFor="demo-token" className="text-sm font-medium">Access token</label>
            <input id="demo-token" type="password" className={inputClass} value={token} required
              autoComplete="current-password" maxLength={2048} disabled={busy}
              onChange={(event) => setToken(event.target.value)} />
          </div>
          {error && <div role="alert"><Notice tone="error">{error}</Notice></div>}
          <button type="submit" disabled={busy || !token.trim()}
            className="console-button-primary inline-flex min-h-10 items-center justify-center gap-2 rounded-sm px-3 py-2 text-sm font-semibold disabled:opacity-50">
            <KeyRound size={16} aria-hidden="true" /> {busy ? "Signing in…" : "Sign in"}
          </button>
        </form>
      </section>
      <p className="console-muted text-sm">Local demo tokens are listed in the project README.</p>
    </main>
  );
}
