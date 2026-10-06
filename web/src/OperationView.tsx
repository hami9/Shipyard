import { useEffect, useRef } from "react";
import type { Client } from "./api/client.ts";
import type { Operation } from "./api/schema.ts";
import { ago, clock, opTone } from "./format.ts";
import { Link } from "./nav.tsx";
import { useApi } from "./useApi.ts";
import { useStream } from "./useStream.ts";

/**
 * OperationView is an operation and its events, live until it ends: the
 * stream resumes after a dropped connection without losing or repeating an
 * event (Last-Event-ID), and its end event carries the final status.
 */
export function OperationView({ client, id }: { client: Client; id: string }) {
  const op = useApi(() => client.call("getOperation", { path: { id } }), [client, id]);
  // {app} takes an ID as well as a slug.
  const appID = op.data?.app_id;
  const app = useApi(
    () => (appID ? client.call("getApp", { path: { app: appID } }) : Promise.resolve(undefined)),
    [client, appID],
  );
  const stream = useStream(client, "streamEvents", { path: { id } }, id);

  const end = stream.events.find((e) => e.event === "end");
  const final: Operation | undefined = end?.event === "end" ? end.data : undefined;
  const current = final ?? op.data;
  const lines = stream.events.flatMap((e) => (e.event === "message" ? [e.data] : []));

  return (
    <section>
      <p className="crumbs">
        <Link to={{ page: "apps" }}>Apps</Link>
        {app.data && (
          <>
            {" / "}
            <Link to={{ page: "app", slug: app.data.slug }}>{app.data.slug}</Link>
          </>
        )}
        {" / "}operation {id.slice(0, 8)}
      </p>
      {op.error && <p className="error" role="alert">{op.error === "Not found." ? "There is no such operation, or its app was deleted." : op.error}</p>}
      {current && (
        <div className="title">
          <h1>
            {current.kind} <span className={`badge ${opTone(current.status)}`}>{current.status}</span>
          </h1>
          <span className="hint">
            {current.phase ? `phase ${current.phase} · ` : ""}attempt {current.attempt} of {current.max_attempts} · queued{" "}
            {ago(current.created_at)}
          </span>
        </div>
      )}
      {current?.last_error && <p className="error">{current.last_error}</p>}
      <EventLog lines={lines.map((e) => ({ key: String(e.seq), ts: e.ts, tone: e.level, text: e.message }))} live={!final} />
      <p className="hint" role="status">
        {stream.state === "connecting" && "Connecting…"}
        {stream.state === "open" && !final && "Live: new events appear as the worker logs them."}
        {final && `Finished ${final.finished_at ? ago(final.finished_at) : ""}.`}
        {stream.state === "ended" && !final && "The stream closed."}
        {stream.state === "failed" && stream.error}
      </p>
    </section>
  );
}

export interface Line {
  key: string;
  ts: string;
  /** A class: an event level, or stderr. */
  tone: string;
  text: string;
}

/** EventLog is a list of timed lines that follows the bottom while live, unless the reader scrolled up. */
export function EventLog({ lines, live }: { lines: Line[]; live: boolean }) {
  const box = useRef<HTMLOListElement>(null);
  const pinned = useRef(true);
  useEffect(() => {
    const el = box.current;
    if (el && live && pinned.current) {
      el.scrollTop = el.scrollHeight;
    }
  }, [lines.length, live]);
  if (lines.length === 0) {
    return <p className="hint">No events yet.</p>;
  }
  return (
    <ol
      className="log"
      ref={box}
      onScroll={(e) => {
        const el = e.currentTarget;
        pinned.current = el.scrollHeight - el.scrollTop - el.clientHeight < 24;
      }}
    >
      {lines.map((l) => (
        <li key={l.key} className={l.tone}>
          <time dateTime={l.ts}>{clock(l.ts)}</time>
          <span>{l.text}</span>
        </li>
      ))}
    </ol>
  );
}
