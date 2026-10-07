// A stand-in for the worker's log socket (ADR-0008): the API reads app logs
// from it, so the logs page can be tested without Docker or a worker. It
// answers GET /logs?app=&tail=&follow= with NDJSON lines, then an end line.

import { rmSync } from "node:fs";
import http from "node:http";

export function workerLogs(socket) {
  const server = http.createServer(async (req, res) => {
    const q = new URL(req.url ?? "/", "http://worker").searchParams;
    const tail = Number(q.get("tail") ?? 100);
    const follow = q.get("follow") === "true";
    const line = (text, stream = "stdout") => JSON.stringify({ ts: new Date().toISOString(), stream, line: text }) + "\n";
    res.writeHead(200, { "Content-Type": "application/x-ndjson" });
    for (let i = 1; i <= Math.min(tail, 3); i++) {
      res.write(line(`tail line ${i}`));
    }
    res.write(line("warning: slow request", "stderr"));
    if (follow) {
      for (let i = 1; i <= 2; i++) {
        await new Promise((r) => setTimeout(r, 300));
        res.write(line(`live line ${i}`));
      }
      res.end(JSON.stringify({ end: "the container stopped" }) + "\n");
    } else {
      res.end(JSON.stringify({ end: "end of the requested lines" }) + "\n");
    }
  });
  rmSync(socket, { force: true });
  return new Promise((resolve) => server.listen(socket, () => resolve(server)));
}
