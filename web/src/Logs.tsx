import { useState } from "react";
import type { Client } from "./api/client.ts";
import type { Scope } from "./api/schema.ts";
import { AppFrame } from "./AppFrame.tsx";
import { logAdvice } from "./phases.ts";
import { EventLog } from "./OperationView.tsx";
import { useStream } from "./useStream.ts";

const tails = [100, 500, 1000];

/**
 * Logs is the running release's output: the last lines, then new ones while
 * following. Container logs cannot resume, so a restart begins with a fresh
 * tail (ADR-0008); the worker has already redacted known secret values.
 */
export function Logs({ client, slug, scopes }: { client: Client; slug: string; scopes: Scope[] }) {
  const [tail, setTail] = useState(100);
  const [follow, setFollow] = useState(true);
  const [run, setRun] = useState(1); // a new number restarts the stream
  const [on, setOn] = useState(true);
  const stream = useStream(client, "streamLogs", { path: { app: slug }, query: { tail, follow } }, `${slug}:${tail}:${follow}:${run}`, on);

  let n = 0;
  const lines = stream.events.flatMap((e) =>
    e.event === "message" ? [{ key: String(n++), ts: e.data.ts, tone: e.data.stream === "stderr" ? "stderr" : "", text: e.data.line }] : [],
  );
  const end = stream.events.find((e) => e.event === "end");
  const live = on && (stream.state === "open" || stream.state === "connecting") && !end;

  return (
    <AppFrame
      slug={slug}
      section="logs"
      scopes={scopes}
      aside={
        <div className="actions">
          <label>
            Last{" "}
            <select value={tail} onChange={(e) => setTail(Number(e.target.value))}>
              {tails.map((t) => (
                <option key={t} value={t}>
                  {t}
                </option>
              ))}
            </select>{" "}
            lines
          </label>
          <label>
            <input type="checkbox" checked={follow} onChange={(e) => setFollow(e.target.checked)} /> Follow
          </label>
          {live ? (
            <button type="button" onClick={() => setOn(false)}>
              Stop
            </button>
          ) : (
            <button
              type="button"
              onClick={() => {
                setOn(true);
                setRun((r) => r + 1);
              }}
            >
              Reconnect logs
            </button>
          )}
        </div>
      }
    >
      {stream.state === "failed" && (
        <div className="callout bad">
          <p className="error" role="alert">
            {stream.error === "Not found." ? `${slug} has no running release, or no such app.` : stream.error}
          </p>
          <p>{logAdvice(stream.status, slug)}</p>
        </div>
      )}
      {stream.state !== "failed" && <EventLog lines={lines} live={live} label={`${slug} output`} quiet />}
      <p className="hint" role="status">
        {stream.state === "connecting" && on && "Connecting…"}
        {live && stream.state === "open" && (follow ? "Following: new lines appear as the app writes them." : "")}
        {end?.event === "end" && `The stream ended: ${end.data.reason}.`}
        {!on && "Stopped."}
      </p>
    </AppFrame>
  );
}
