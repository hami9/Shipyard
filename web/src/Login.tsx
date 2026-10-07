import { useEffect, useState, type FormEvent } from "react";
import { newClient, signIn, type Session } from "./session.ts";

interface Props {
  /** A token saved earlier in this tab, signed in again without asking. */
  resume: string | undefined;
  notice: string | undefined;
  onSignedIn: (s: Session) => void;
  /** The saved token no longer works. */
  onRejected: () => void;
}

export function Login({ resume, notice, onSignedIn, onRejected }: Props) {
  const [token, setToken] = useState("");
  const [error, setError] = useState<string | undefined>();
  const [busy, setBusy] = useState(resume !== undefined);

  async function attempt(raw: string, saved: boolean) {
    setBusy(true);
    setError(undefined);
    const res = await signIn(raw, (t) => newClient(t));
    setBusy(false);
    if (typeof res === "string") {
      if (saved) {
        onRejected();
      }
      setError(res);
      return;
    }
    setToken("");
    onSignedIn(res);
  }

  // Once per saved token. StrictMode runs this twice in development: two
  // identical whoami calls, harmless.
  useEffect(() => {
    if (resume !== undefined) {
      void attempt(resume, true);
    }
  }, [resume]);

  useEffect(() => {
    document.title = "Sign in · Shipyard";
  }, []);

  function submit(e: FormEvent) {
    e.preventDefault();
    void attempt(token, false);
  }

  return (
    <main className="login">
      <h1>Shipyard</h1>
      <form onSubmit={submit}>
        <label htmlFor="token">API token</label>
        <input
          id="token"
          type="password"
          autoComplete="off"
          spellCheck={false}
          placeholder="shp_…"
          value={token}
          onChange={(e) => setToken(e.target.value)}
          disabled={busy}
          required
          // The page's one field: start there.
          autoFocus
          aria-invalid={error ? true : undefined}
          aria-describedby={error ?? notice ? "token-error" : undefined}
        />
        <button type="submit" disabled={busy || token.trim() === ""}>
          {busy ? "Checking…" : "Sign in"}
        </button>
        {(error ?? notice) && (
          <p id="token-error" className="error" role="alert">
            {error ?? notice}
          </p>
        )}
      </form>
      <p className="hint">
        Create a token on the server with <code>shipyard-api token create</code>. It stays in this tab only, until you
        sign out or close it.
      </p>
    </main>
  );
}
