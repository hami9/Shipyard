import { useRef, useState, type FormEvent } from "react";
import { useDialogFocus } from "./a11y.tsx";
import type { Client } from "./api/client.ts";
import { explain } from "./forms.ts";
import { navigate } from "./nav.tsx";

const shaRE = /^([0-9a-f]{40}|[0-9a-f]{64})$/;

/**
 * Deploy queues a deploy of the branch head, or of a commit on the branch
 * (invariant 7: the worker checks it), and opens the operation to follow it.
 */
export function Deploy({ client, slug, branch }: { client: Client; slug: string; branch: string }) {
  const [open, setOpen] = useState(false);
  const trigger = useRef<HTMLButtonElement>(null);
  if (!open) {
    return (
      <button type="button" className="primary" ref={trigger} onClick={() => setOpen(true)}>
        Deploy
      </button>
    );
  }
  // The button is replaced by the form, so closing it hands focus back here.
  const close = () => {
    setOpen(false);
    requestAnimationFrame(() => trigger.current?.focus());
  };
  return <DeployForm client={client} slug={slug} branch={branch} onClose={close} />;
}

// One Idempotency-Key per opened form, so a double click queues it once.
function DeployForm({ client, slug, branch, onClose }: { client: Client; slug: string; branch: string; onClose: () => void }) {
  const [ref, setRef] = useState("");
  const [key] = useState(() => crypto.randomUUID());
  const [error, setError] = useState<string | undefined>();
  const [busy, setBusy] = useState(false);
  const input = useRef<HTMLInputElement>(null);
  const dialog = useDialogFocus<HTMLFormElement>(onClose, input);

  async function submit(e: FormEvent) {
    e.preventDefault();
    const sha = ref.trim().toLowerCase();
    if (sha !== "" && !shaRE.test(sha)) {
      setError("A full commit SHA: 40 (or 64) hex characters. Leave it empty for the branch head.");
      input.current?.focus();
      return;
    }
    setBusy(true);
    setError(undefined);
    try {
      const res = await client.call("deploy", {
        path: { app: slug },
        header: { "Idempotency-Key": `${key}-${sha || "head"}` },
        ...(sha ? { body: { ref: sha } } : {}),
      });
      navigate({ page: "operation", id: res.operation.id });
    } catch (err) {
      setError(explain(err).fields["ref"] ?? explain(err).message);
      setBusy(false);
    }
  }

  return (
    <form className="confirm" onSubmit={submit} aria-labelledby="deploy-title" {...dialog}>
      <h3 id="deploy-title">Deploy {slug}</h3>
      <label htmlFor="deploy-ref">
        Commit <span className="hint">(empty: the head of {branch})</span>
      </label>
      <input
        id="deploy-ref"
        ref={input}
        value={ref}
        onChange={(e) => setRef(e.target.value)}
        autoComplete="off"
        spellCheck={false}
        placeholder="full SHA, optional"
        aria-invalid={error ? true : undefined}
        aria-describedby={error ? "deploy-error" : undefined}
      />
      <div className="actions">
        <button type="submit" disabled={busy}>
          {busy ? "Queuing…" : "Deploy"}
        </button>
        <button type="button" onClick={onClose} disabled={busy}>
          Cancel
        </button>
      </div>
      {error && (
        <p id="deploy-error" className="error" role="alert">
          {error}
        </p>
      )}
    </form>
  );
}
