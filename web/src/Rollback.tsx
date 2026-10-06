import { useState } from "react";
import type { Client } from "./api/client.ts";
import type { Admitted, Release } from "./api/schema.ts";
import { shortSHA } from "./format.ts";
import { needsConfigChoice, rollbackBody, type ConfigChoice } from "./rollbackRules.ts";
import { message } from "./useApi.ts";

interface Props {
  client: Client;
  slug: string;
  target: Release;
  onDone: (a: Admitted) => void;
  onCancel: () => void;
}

/**
 * Rollback confirms a rollback to target and queues it. One Idempotency-Key
 * per confirmation, so a double click or a retry queues it once. If secrets
 * changed since the target ran, the API asks which values it gets.
 */
export function Rollback({ client, slug, target, onDone, onCancel }: Props) {
  const [key] = useState(() => crypto.randomUUID());
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | undefined>();
  const [choose, setChoose] = useState(false);

  async function submit(choice?: ConfigChoice) {
    setBusy(true);
    setError(undefined);
    try {
      const res = await client.call("rollback", {
        path: { app: slug },
        // A changed choice is a different request, so it gets its own key.
        header: { "Idempotency-Key": choice ? `${key}-${choice}` : key },
        body: rollbackBody(target.id, choice),
      });
      onDone(res);
    } catch (err) {
      if (!choice && needsConfigChoice(err)) {
        setChoose(true);
      } else {
        setError(message(err));
      }
    } finally {
      setBusy(false);
    }
  }

  return (
    <div className="confirm" role="dialog" aria-labelledby="rollback-title">
      <h3 id="rollback-title">
        Roll back {slug} to release <code>{target.id.slice(0, 8)}</code>?
      </h3>
      <p>
        Its image (commit <code>{shortSHA(target.commit)}</code>) runs again; nothing is rebuilt. It goes through the
        health check and takes traffic only if healthy.
      </p>
      {choose ? (
        <>
          <p>Secrets changed since that release ran. Which values should it get?</p>
          <div className="actions">
            <button type="button" onClick={() => void submit("current")} disabled={busy}>
              Today's values
            </button>
            <button type="button" onClick={() => void submit("old")} disabled={busy}>
              The values it ran with
            </button>
            <button type="button" onClick={onCancel} disabled={busy}>
              Cancel
            </button>
          </div>
        </>
      ) : (
        <div className="actions">
          <button type="submit" onClick={() => void submit()} disabled={busy}>
            {busy ? "Queuing…" : "Roll back"}
          </button>
          <button type="button" onClick={onCancel} disabled={busy}>
            Cancel
          </button>
        </div>
      )}
      {error && <p className="error" role="alert">{error}</p>}
    </div>
  );
}
