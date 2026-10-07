import { useEffect, useRef } from "react";
import type { Client } from "./api/client.ts";
import type { Operation } from "./api/schema.ts";
import { ago, clock, opTone, shortSHA } from "./format.ts";
import { Link } from "./nav.tsx";
import { advice, failedPhase, steps, type StepState } from "./phases.ts";

// A step's state in words, for screen readers; the page shows it in color and shape.
const stepWords: Record<StepState, string> = { done: "done", current: "in progress", failed: "failed", todo: "not reached" };
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

  // The release this deploy or rollback made carries the failure's reason;
  // look for it among the newest, again once the operation ends.
  const releases = useApi(
    () =>
      appID && current?.kind !== "delete"
        ? client.call("listReleases", { path: { app: appID }, query: { limit: 20 } })
        : Promise.resolve(undefined),
    [client, appID, current?.kind, final?.status],
  );
  const release = releases.data?.deployments.find((r) => r.operation_id === id);
  const reason = release?.failure_reason ?? current?.last_error;
  const failed = current?.status === "failed";
  const where = failed ? failedPhase(current?.phase, reason) : undefined;
  const plan = current ? steps({ kind: current.kind, status: current.status, phase: current.phase ?? where }) : [];

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
            attempt {current.attempt} of {current.max_attempts} · queued {ago(current.created_at)}
            {release && (
              <>
                {" · commit "}
                <code title={release.commit}>{shortSHA(release.commit)}</code>
              </>
            )}
          </span>
        </div>
      )}
      {failed && (
        <div className="callout bad">
          <h2>{current?.kind === "delete" ? "The delete failed" : "This release failed"}</h2>
          {reason && <p className="reason-text">{reason}</p>}
          {advice(where) && <p>{advice(where)}</p>}
          {current?.kind !== "delete" && <p className="hint">A failed release never takes traffic: the one serving before kept serving.</p>}
          {app.data && (
            <p className="actions">
              <Link to={{ page: "logs", slug: app.data.slug }} className="button small">
                App logs
              </Link>
              <Link to={{ page: "settings", slug: app.data.slug }} className="button small">
                Settings
              </Link>
            </p>
          )}
        </div>
      )}
      {!failed && current?.last_error && <p className="notice">The last attempt failed, and it will be retried: {current.last_error}</p>}
      {plan.length > 0 && (
        <ol className="steps" aria-label="Steps">
          {plan.map((s) => (
            <li key={s.phase} className={s.state}>
              <span className="sr-only">{stepWords[s.state]}: </span>
              {s.label}
            </li>
          ))}
        </ol>
      )}
      <h2 className="section-title">Events</h2>
      <EventLog lines={lines.map((e) => ({ key: String(e.seq), ts: e.ts, tone: e.level, text: e.message }))} live={!final} label="Events" />
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

/**
 * EventLog is a list of timed lines that follows the bottom while live,
 * unless the reader scrolled up. It is a scrolling region, so it takes
 * keyboard focus (WCAG 2.1.1), and a log: new lines are announced politely,
 * unless quiet (an app's output would drown a screen reader).
 */
export function EventLog({ lines, live, label, quiet = false }: { lines: Line[]; live: boolean; label: string; quiet?: boolean }) {
  const box = useRef<HTMLDivElement>(null);
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
    <div
      className="log"
      role="log"
      aria-label={label}
      aria-live={quiet ? "off" : "polite"}
      tabIndex={0}
      ref={box}
      onScroll={(e) => {
        const el = e.currentTarget;
        pinned.current = el.scrollHeight - el.scrollTop - el.clientHeight < 24;
      }}
    >
      <ol>
        {lines.map((l) => (
          <li key={l.key} className={l.tone}>
            <time dateTime={l.ts}>{clock(l.ts)}</time>
            <span>{l.text}</span>
          </li>
        ))}
      </ol>
    </div>
  );
}
