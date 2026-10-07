# Work Log

A chronological record of work on Shipyard, **newest entry first**. Every working session adds one entry. The log is the hand-off between sessions and between people and agents. Someone new should be able to resume from the `Current status` block plus the newest entry alone.

## Current status

> Update this block at the end of every session.

| Field | Value |
| --- | --- |
| **Active phase** | Phase 5: Hardening, install, and v1.0. Phase 4 is done in code; its exit criteria need a real GitHub App and VPS (the owner's). Phase 3 is done except its exit criterion on a real VPS (the owner's restore drill). Phase 2 is done except two exit criteria that need the owner (a real certificate on a VPS; whether the logs-resume criterion means `events`). Stacked PRs, merge in order: #1 `schema-v1` (P1.1–P1.3) → `main`; #2 `env-secrets` (P1.4); #3 `op-queue` (P1.5); #4 `app-api` (P1.6); #5 `cli` (P1.7); #6 `source-fetch` (P1.8); #7 `image-build` (P1.9); #8 `container-runtime` (P1.10); #9 `deploy-worker` (P1.11); #10 `caddy-edge` (P2.1); #11 `route-render` (P2.2); #12 `caddy-admin` (P2.3); #13 `traffic-switch` (P2.4); #14 `domain-api` (P2.5); #15 `drain-window` (P2.6); #16 `event-stream` (P2.7a); #17 `app-logs` (P2.7b); #18 `api-edge` (P2.8); #19 `exit-checks` (Phase 2 exit checks and review fixes; opened against `main`); #20 `releases` (P3.1); #21 `reconcile` (P3.2); #22 `rollback` (P3.3); #23 `retention` (P3.4a, opened against `main`); `retention-caps` (P3.4b, pushed, no PR yet); `backup` (P3.5, pushed, no PR yet); `rebuild` (P3.6a, pushed, no PR yet); `restore` (P3.6b, pushed, no PR yet); `crash-suite` (P3.7, pushed, no PR yet); `app-delete` (P3.8, pushed, no PR yet); `webhook-verify` (P4.1, pushed, no PR yet); `push-deploy` (P4.2, P4.3, pushed, no PR yet); `github-app` (P4.4, pushed, no PR yet); `catch-up` (P4.5, pushed, no PR yet); `deploy-status` (P4.6, pushed, no PR yet); #24 `build-network` (P5.1, opened against `main` at the owner's request 2026-10-04, so it carries the whole stack); `rootless-build` (P5.2, pushed, no PR yet); `rate-limit` (P5.3a, pushed, no PR yet); `token-rotate` (P5.3b, pushed, no PR yet); `kek-rotate` (P5.4a, pushed, no PR yet); `hpke-seal` (P5.4b, pushed, no PR yet); `metrics` (P5.5a, pushed, no PR yet); `app-health` (P5.5b, pushed, no PR yet); `disk-cert` (P5.6, pushed, no PR yet); `install-script` (P5.7a, pushed, no PR yet); `installer` (P5.7b, pushed, no PR yet); `ops-guide` (P5.7c, pushed, no PR yet); `security-review` (P5.8a, pushed, no PR yet); `caddy-group` (P5.8b, pushed, no PR yet); `acceptance-demo` (P5.9a, pushed, no PR yet); `openapi` (P6.1a, pushed, no PR yet); `ts-client` (P6.1b, pushed, no PR yet, CI green); `web-app` (P6.2a, pushed, no PR yet, CI green); `web-serve` (P6.2b, pushed, no PR yet); `app-pages` (P6.3a, pushed, no PR yet); `ui-rollback` (P6.3b, pushed, no PR yet); `live-events` (P6.4, pushed, no PR yet); `env-domains` (P6.5, pushed, no PR yet); `app-forms` (P6.5b, pushed, no PR yet); `ui-tokens` (P6.5c, pushed, no PR yet); `ui-e2e` (P6.6a, pushed, no PR yet); `ui-flows` (P6.6b, pushed, no PR yet); `ui-a11y` (P6.6c, pushed, no PR yet); `ui-ci` (P6.6d). Merging the stack is the owner's step: an agent-run merge was blocked by the permission classifier on 2026-09-28 |
| **Last completed** | P6.6d: the UI tests in CI, so Phase 6 is done. Before it, P6.6c: the manual accessibility pass and its fixes (19 Playwright tests); P6.6b: the UI's flows end to end; P6.6a: the harness and axe on every page; P6.5c: tokens in the UI, so every CLI flow but install is there; P6.5b: create, change and delete apps, and deploy, in the UI; P6.5: environment (write-only values) and domains; P6.4: live operation events and logs; P6.3: the app list, app detail, release history (P6.3a), and rollback (P6.3b) in the UI. Before it, P6.2: the React app with the token login (P6.2a), served by Caddy on the API hostname with a strict CSP and shipped in the server archive (P6.2b, ADR-0016). Before it, P6.1: `api/openapi.json`, held to the handlers by tests (P6.1a), and the TypeScript client generated from it (P6.1b, ADR-0015). Before it, P5.9a: the acceptance runbook (docs/ACCEPTANCE.md), the sample app (examples/hello), and the server archive's directory fix. Before it, P5.8b: the `shipyard-caddy` group for Caddy's admin socket, so P5.8 is done; P5.8a: security review (docs/security-review.md), govulncheck, fixes; P5.7: installer, build firewall, operator guide; P5.6: disk and certificate checks with log warnings and shipped Prometheus alert rules at 80%, ADR-0014 (P5.5 metrics before it, ADR-0013) |
| **Next task** | The owner's: P5.9b (the acceptance demo on fresh VPSes), merging the stack, and `v1.0.0`. Then P7 (unscheduled). In parallel, P5.9b (the owner's): run docs/ACCEPTANCE.md on two fresh VPSes and record it there; then merge the stack and tag `v1.0.0`. It also covers: a real `install.sh` run on a fresh VPS; the Phase 4 exit criteria with a real GitHub App, and the Phase 3 restore drill, on a real VPS |
| **Blockers** | None |
| **Open risks** | Builder egress is unrestricted (ADR-0009). The builder container is still privileged, though rootless (ADR-0010); Ubuntu 24.04+ hosts need the userns sysctl (`deploy/sysctl/`), untested on a real Ubuntu kernel. On Docker Desktop (macOS/Windows), Phase 1+ health probes cannot reach container IPs `[DK-DESKTOP-NET]`. Images that start as root and drop privileges (e.g. stock nginx) may need allowlisted capabilities, which have no per-app setting yet. A delete that fails midway leaves the app out of service until it is deleted again. Pushes match apps by repository name and an app's repo is fixed, so a renamed repository stops deploying until P4.4. `webhook_deliveries` has no retention yet (one small row per push) |
| **Last updated** | 2026-10-06 |

## Entry template

Copy this block to the top of the entries section.

```markdown
### YYYY-MM-DD: <short title>

- **Phase / task:** P<n>.<m>: <roadmap item>
- **Author:** <human name or agent + session link>
- **Goal:** one sentence.

**Done**
- …

**Changed files**
- `path`: why

**Decisions**
- … (link ADR if one was written; "none" is valid)

**Verification**
- `command`: result (paste the real summary: pass/fail counts, errors)

**Problems / surprises**
- … (include source tags if vendor behavior differed from the docs)

**Next**
- The exact next step and its command, so the next session can start without guessing.
```

**Rules**

- Record facts, not intentions. If something was not run, say "not run".
- Never paste secrets, tokens, full env files, or customer data.
- Link commits by short hash once pushed.
- Keep entries short, around 10–25 lines. Move long analysis to an ADR or `docs/`.

## Entries

### 2026-10-07: P6.6d UI tests in CI

- **Phase / task:** P6.6d: the UI's Playwright tests in CI; P6.6, and with it Phase 6, is done
- **Author:** Claude Code (desktop session)

**Done**
- **The `ui` job in `ci.yml`** (the owner's choice: CI and local):
  - a PostgreSQL 18 service; Go from `go.mod`; Node 24;
  - `npm ci`, `npx playwright install --with-deps chromium`, `npx playwright test`;
  - on a failure, it keeps the report and traces 7 days (`actions/upload-artifact@v7`; the tag and its `name`, `path` and `retention-days` inputs checked).
  - No Docker and no worker, so it stays within the rule that Docker-based e2e tests stay out of CI.
- **Fix:** `test/uiseed/main.go` needed `gofmt` after P6.6b's edit. CI's lint caught it; I had not rerun Go lint after that change. It is fixed here, at the tip of the stack: `ui-flows` and `ui-a11y` alone still fail lint until this branch is merged after them.

**Verification**
- **CI run 37606996760** (`ui-ci`, before the fix):
  - **UI end to end: success, 19 passed (57.9 s)** on Linux, all-in-one: the seeder, the API, and the stand-in log socket, so the full logs path (tail, stderr, live, end) ran for the first time.
  - Integration and Web client: success.
  - Lint: failure (`gofmt needed on: test/uiseed/main.go`).
- **After the fix:** `make lint` and `go test ./...` exit 0 (WSL). This push's CI: CI_RESULT.
- **Phase 6 exit criteria:**
  - Every CLI flow but install is in the UI (P6.5c's check).
  - The e2e suite is green: 19 Playwright tests in CI.

**Next**
- **Phase 6 is done.** What remains is the owner's: P5.9b, the acceptance demo on fresh VPSes, then merging the stack and `v1.0.0`. After that, P7 (unscheduled).

### 2026-10-07: P6.6c manual accessibility pass

- **Phase / task:** P6.6c: the UI's manual accessibility pass and its fixes
- **Author:** Claude Code (desktop session)

**Found by hand** (behavior axe cannot see), **and fixed:**
- **Page changes without a reload** left focus on a link that was gone, announced nothing, and kept the title "Shipyard" (WCAG 2.4.2, 2.4.3).
  - Fix: `title(route)` gives each page its own title. Shell sets it, moves focus to `<main>`, and announces it in a polite live region. The login's title is "Sign in · Shipyard".
- **No way past the header** (2.4.1). Fix: a "Skip to content" link, the first tab stop, shown on focus.
- **Field hints and errors were not tied to their inputs** in the app forms, and after a failed submit focus stayed on the button (3.3.1, 1.3.1).
  - Fix: `aria-describedby` and `aria-invalid` on every field, and `focusFirstInvalid` on app, environment and domain forms.
  - The forms have `noValidate`, so the page's own messages show, not the browser's.
- **Inline confirmations** replaced the button that opened them, losing focus, and could not be dismissed by key. Fix: `ConfirmButton` focuses Cancel, closes on Escape, and returns focus to its button. After a confirmed removal the row is gone, so focus goes to the page heading.
- **The rollback and deploy panels** took no focus and ignored Escape. Fix: `useDialogFocus` (into the panel or its field; Escape closes; focus returns); Deploy hands focus back to its button itself.
- **The log box** scrolled but could not be reached by keyboard (2.1.1).
  - Fix: `role="log"` with a label and `tabIndex=0`; operation events `aria-live="polite"`; app output `aria-live="off"`, since it would drown a screen reader.
- **Focus:** a `:focus-visible` ring in both schemes (2.4.7); none on focus the page moved itself. The login field has `autoFocus`, and its error describes it.

**Verification**
- `tsc` (both configs) exit 0; `node --test` 36 of 36 pass (new: a distinct title per page).
- **`e2e/a11y.spec.ts`**, all by keyboard:
  - titles; Tab → the skip link; Enter → focus in main;
  - Enter on a link → new title, focus on main, the announcement; Back → the previous title;
  - the empty New app form → focus on Name, `aria-invalid`, its description "is required", and Branch described by its hint;
  - Remove → Cancel focused; Escape → Remove focused; confirm → focus on the heading;
  - Roll back → the dialog focused; Escape → closed, the button focused; Deploy → its field focused; Escape → the button focused;
  - the events log is labelled, focusable and polite.
- **`npx playwright test`:** **19 of 19 passed** (50.9 s), axe still clean on every page in both schemes.

**Next**
- P6.6d: the CI job.

### 2026-10-07: P6.6b UI flows end to end

- **Phase / task:** P6.6b: the UI's flows in Playwright, against the real API
- **Author:** Claude Code (desktop session)

**Done**
- **`e2e/flows.spec.ts`** (serial, on the shared seed):
  - **App:** a duplicate name → 409; branch `-bad` → 422 under its field (axe on the form with errors); create `shop`; Save → "Saved", Save again → "Nothing changed."; delete enabled only by the exact name, then the delete operation's page.
  - **Deploy** with the deploy token: a short SHA refused in the page (axe), then the branch head → the live operation page.
  - **Rollback:** no button on the active or failed release; the dialog (axe) → "Rollback queued" → "Follow it" → the operation; "Older releases" → 25.
  - **Environment:** a bad key refused (axe); a secret set. Its value is in neither the PUT's response nor the page's HTML, and the field is cleared; then removed.
  - **Domains:** `bad host` → 422 (axe); added, lowercased; the same name on `docs` → 409; removed.
  - **Logs:** with the stand-in worker, the tail, the stderr line, live lines, the end reason, and Follow off (axe). On Windows, the API's 503.
  - **A read token:** no Deploy, Roll back, Settings, New app, forms or Remove; tokens "need the admin scope".
- **`e2e/tokens.spec.ts`:**
  - **Rotate,** keeping the old token 1 h: the new token is shown and stored, and survives a reload while the display goes. Rotate again, ending the old → "no longer works", and the app pages still load.
  - **Revoke** another token, then the session's own → signed out with the reason, storage empty.
- **`test/uiseed`:** three more tokens only the token tests use and end (`rotate`, `spare`, `doomed`), so no other test depends on the order files run in.
- `web/e2e/serve-api.sh` is executable in git now (committed from Windows without the bit).

**Verification**
- `tsc` (both configs) exit 0.
- **`npx playwright test`** (Windows, Chromium, API from `serve-api.sh` in WSL): **14 of 14 passed** (50.5 s). The first run had 1 failure from my selector: `getByLabel("Branch")` also matched "Deploy on every push to the branch". The labels are exact now.
- **Not run:** logs through the stand-in (Linux only: P6.6d's CI job).

**Problems / surprises**
- **Stopping `serve-api.sh` from Windows** (killing `wsl.exe`) does not reach its `trap`; a leftover `shipyard_ui_*` database was dropped by hand. Stop it with Ctrl-C in WSL.

**Next**
- P6.6c: the manual accessibility pass.

### 2026-10-07: P6.6a UI test harness and axe

- **Phase / task:** P6.6a: the Playwright harness, and axe on every page (P6.6 split: b flows, c manual pass, d CI)
- **Author:** Claude Code (desktop session)

**Done**
- **The owner's choices (2026-10-07):** in CI and locally; Chromium only; axe plus a manual pass.
- **Dependencies** (exact, development only): `@playwright/test` 1.63.0 (Apache-2.0), `@axe-core/playwright` 4.13.0 (MPL-2.0, never shipped).
- **`test/uiseed` (Go, test tooling):** `create KEKDIR` makes a throwaway `shipyard_ui_*` database, migrates and seeds it, and writes an HPKE public key. It prints JSON, including `routing.WebCSP`, so the tests serve the CSP from a single source. `drop NAME` removes the database.
  - The seed: `web` and `docs`; web's 25 releases with their operations and one event each; tokens for admin, deploy and read.
  - Only the public key is written: the API never decrypts (ADR-0012), and a public key has no file-mode check, which Windows cannot satisfy.
- **`web/e2e`:**
  - `setup.mjs`: seeds; builds `shipyard-api` and the UI; starts the API (preflight and rate limits off); starts the stand-in worker log socket; serves `dist/` like Caddy. It passes tokens and the URL through the environment, and tears everything down, the database included.
  - `server.mjs`: the production build, with the index fallback, the CSP and headers, and `/v1` proxied.
  - `worker-logs.mjs`: ADR-0008's NDJSON on a Unix socket. Not on Windows, where Node serves named pipes only `[NODE-IPC]`.
  - `serve-api.sh`: the API side in WSL, for Playwright on Windows (`SHIPYARD_UI_EXTERNAL`).
  - `helpers.ts`: `signIn` through the form; `accessible` (axe, WCAG 2.0–2.2 A and AA); `cspViolations`.
  - `pages.spec.ts`: the login page and every signed-in page, an operation's included, in light and dark, each checked by axe with no CSP violation; and the login flow (a wrong token, a reload, sign-out).
- **`tsconfig.e2e.json`:** Playwright's declarations need Node's types, so the e2e config has `skipLibCheck` and an esnext lib, instead of adding `@types/node`. `npm run check` runs both configs.
- `make test-ui`; `.gitignore` for Playwright's output and the external file.
- **Fix found by axe:** in dark mode, primary and Delete buttons had white text on light fills (about 2.3:1). Text on fills is now `--on-fill`: white in light, near-black in dark.

**Problems / surprises**
- **The dev database is unreachable from Windows:** Docker in this WSL publishes ports by iptables alone, with no `docker-proxy` and no WSL listener to forward. The external mode avoids it; the owner's Docker settings were left alone.

**Verification**
- `tsc` (both configs) exit 0; `make lint` and `go test ./...` exit 0 (WSL); `serve-api.sh` passes `bash -n`.
- **`npx playwright test`** (Windows, Chromium; API from `serve-api.sh` in WSL): first run 4 passed, 1 failed (axe: color-contrast on the settings page's Save, dark); after the fix, **5 of 5 passed** (33 s).
- **Not run:** the all-in-one mode on Linux (CI, P6.6d); logs through the stand-in (Linux only).

**Next**
- P6.6b: the flows end to end.

### 2026-10-07: P6.5c tokens in the UI

- **Phase / task:** P6.5c: list, revoke and rotate tokens in the UI; P6.5b is done
- **Author:** Claude Code (desktop session)

**Done**
- **`/tokens`, linked from the header:**
  - **This session's token:** prefix, name, scopes, expiry, and Rotate with a grace for the old one (now, 1 h, 24 h, 7 d; the API's maximum is 7 days).
  - After a rotation, the page uses the new token at once: App's new `onSession` saves it and rebuilds the client. The new token is shown once, with Copy and Hide, for the CLI. The notice says whether the old one still works and until when.
  - **All tokens (admin):** prefix, name, scopes, status, expiry, last use; Revoke asks once more. Revoking the page's own token reads "Revoke and sign out", and does.
  - New tokens stay a server command (`shipyard-api token create`), as in the CLI.
- **`tokens.ts`:** the grace choices, `whoamiOf` (the API's new token as the session's), and status tones.

**Verification**
- `tsc` exit 0; `node --test` 35 of 35 pass; the production build passes.
- **In the browser pane, against the real dev API:**
  - The list showed 7 tokens, the session's marked, and the older test tokens "expired". No hash anywhere.
  - **Rotate, keeping the old token 1 h:** the new token was shown, and it was the one stored in the tab. The list, reloaded with it, marked it as this session; the old one was still "active".
  - After a reload, the page was still signed in with the new token, and the one-time display was gone.
  - **Revoking the old token** → "revoked", and the page stayed signed in.
  - **Rotate, ending it now** → "The old token no longer works.", and the previous token was "revoked".
  - **Revoking the page's own token** → signed out, "You revoked the token this page used.", and the tab's storage was empty.
  - Test tokens dev only, the file deleted; the API and the UI server stopped.
- **The exit criterion, checked against `shipyard`'s usage:** login and whoami, tokens, apps (create, list, show, update, delete), env, domains, deploy, releases, rollback, operation, events and logs are all in the UI now. Only `version` is not, which prints the CLI's own build.

**Next**
- P6.6: accessibility pass and Playwright e2e tests. Playwright is a new dependency (and its browsers): ask the owner first.

### 2026-10-07: P6.5b apps and deploys in the UI

- **Phase / task:** P6.5b: create, change and delete apps, and deploy, in the UI (P6.5b split: P6.5c is tokens)
- **Author:** Claude Code (desktop session)

**Done**
- **The owner's choice (2026-10-07):** deleting an app is in the UI, enabled only once the app's exact name is typed.
- **Routes:**
  - `/new` for a new app. It is not under `/apps/`, since `new` is a valid slug, and `/apps/new` stays that app's page.
  - `/apps/<slug>/settings`.
- **`appForm.ts`:**
  - `valuesOf` (an app as form values, or the schema's defaults);
  - `durationMs` (Go durations: `ms`, `s`, `m`, `h`, fractions, `0`);
  - `createBody`, and `updateBody`, which sends only what changed: memory in MiB, durations compared by value, so `60s` against `1m0s` is no change.
  - The page checks only numbers, durations and required fields. The API checks the rest and names a field the same way.
- **`AppEditor.tsx`:**
  - `NewApp`, which opens the new app's page;
  - `AppSettingsPage`, whose form folds build, limits and timeouts, and opens them when one holds an error;
  - `DangerZone`: type the name; one `Idempotency-Key`; then the delete operation's page.
- **`Deploy.tsx`:** the branch head or a full SHA (checked first), one key per opened panel, then the operation's page.
- **Gates:** New app and Settings need admin; Deploy needs deploy (`canDeploy`, now shared with rollback).

**Verification**
- `tsc` exit 0; `node --test` 34 of 34 pass (routes; durations; the create body and its field problems; only changed fields in an update); the production build passes.
- **In the browser pane, against the real dev API** (admin test token, dev only, file deleted):
  - **Create:**
    - the name `web` → 409, "an app with this slug already exists";
    - branch `-bad` → 422, "must not start with '-'" under Branch;
    - `api` on `release` → the app's page.
  - **Settings:**
    - port 4000, `/healthz`, 1024 MiB and timeout `60s` → PATCH 200; reloaded, `1m0s` was unchanged;
    - a second Save → "Nothing changed.";
    - `15 min` → the error under Stop timeout, with the folded section opened;
    - `30s` → "Saved. The next deploy uses the new settings."
  - **Deploy:** `abc123` was refused in the page; the branch head → `/operations/<id>`, "deploy queued", live.
  - **Delete:** `API` left the button disabled; `api` enabled it → `/operations/<id>`, "delete queued". The dev database has no worker, so it stays queued.
- **Bug found and fixed:** the settings form remounted after a save, keyed on `updated_at`, and so dropped its "Saved." message. The key was not needed.

**Next**
- P6.5c: tokens in the UI.

### 2026-10-07: P6.5 environment and domains in the UI

- **Phase / task:** P6.5: environment management (write-only values) and domains
- **Author:** Claude Code (desktop session)

**Done**
- **Routes:** `/apps/<slug>/env` and `/apps/<slug>/domains`, linked from the app header (Environment, Domains, Logs).
- **`forms.ts`:**
  - `canChange` (admin scope, as the spec requires for writes);
  - `keyProblem` and `valueProblem` (the API's key rule; 64 KiB in UTF-8; no NUL), checked before a request;
  - `explain`: a 422's `errors[]` by field, anything else as one message.
- **`EnvPage`:**
  - the revision and the keys (secret or plain); Remove asks once more.
  - "Set a variable": the key; the value in a password field for a secret (`autocomplete="new-password"`, so it is never autofilled or offered for saving); a Secret checkbox, on by default.
  - The value is cleared once sent, and the API returns keys only.
- **`DomainsPage`:**
  - each hostname (a link), the release serving it, and when its DNS was checked; Remove asks once more.
  - "Add a domain" shows the DNS preflight's field errors under the field.
- **Layout fix:** a table that scrolls at phone width was still widening the page through its hidden column heading (`.sr-only`, absolutely positioned with no positioned ancestor). This came with P6.3b's Actions column. The table is now `position: relative`.
- **Roadmap:** P6.5b added for the CLI flows the UI still lacks, so the phase's exit criterion is met honestly: app create, settings and delete; deploy; token list, revoke and rotate.

**Verification**
- `tsc` exit 0; `node --test` 30 of 30 pass (routes; `canChange`; keys and values, including 64 KiB and multi-byte; `explain`); the production build passes.
- **In the browser pane, against the real dev API** (admin test token, dev only, file deleted):
  - **Environment:**
    - The key `1BAD-KEY` was refused in the page, with no request sent.
    - `API_TOKEN` was set as a secret, giving revision 3. Afterwards the value was in neither field, nowhere in the page's HTML, and not in session or local storage. The PUT's response body is `{"revision":3,"vars":[{"key":"API_TOKEN","secret":true},…]}`: keys only.
    - Removing it asked once more, then gave revision 4.
  - **Domains:**
    - `Shop.Example.com ` → 201; the API stored `shop.example.com`, serving the active release ("not checked": preflight is off in dev).
    - `bad host` → 422, shown under the field.
    - The same hostname on `docs` → 409, "this hostname is already used by an app".
    - Remove → 204, and the list emptied.
  - **Phone width:** the app, environment and domains pages are all 375 px wide at 375 px. Before the fix, the app page was 459.

**Next**
- P6.5b: the remaining CLI flows in the UI (ask the owner whether app deletion belongs in the UI at all; it is not undoable).

### 2026-10-06: P6.4 live events and logs

- **Phase / task:** P6.4: live operation events and logs in the UI
- **Author:** Claude Code (desktop session)

**Done**
- **Routes:** `/operations/<id>` (a lowercase UUID) and `/apps/<slug>/logs`.
- **`useStream`:**
  - follows one stream while a page shows it, and aborts it when the page leaves or its inputs change;
  - batches a burst into one render, keeps at most 2000 events (`appendCapped`);
  - states: connecting, open, ended, stopped, failed.
- **`OperationView`:** the operation (status, phase, attempt, its app resolved from `app_id`, since `{app}` takes an ID), and its events live. The end event's operation becomes the final status.
- **`EventLog`:** timed lines that keep to the bottom while live, unless the reader scrolled up.
- **`Logs`:** tail 100, 500 or 1000, follow, Stop / Start again; stderr marked. A 404 says there is no running release; other errors show the API's message.
- **Links:** Logs in the app header, "events" on each release, "Follow it" after a rollback.
- **Client fixes found by testing:**
  - `stream` now retries a reconnection that fails transiently (network, 5xx) instead of ending; a 4xx is still final.
  - `onOpen` lets a page show "Live" before the first event.

**Decisions**
- None beyond ADR-0015's note.

**Verification**
- `tsc` exit 0; `node --test` 27 of 27 pass (new: routes, `appendCapped`, `clock`, operation tones, `onOpen` per connection, riding out two 502s while resuming with `Last-Event-ID: 1`, and a 404 that is not retried); the production build passes.
- **In the browser pane, against the real dev API.** The data: the queued rollback operation from P6.3b; events inserted into `operation_events` by SQL while the page was open.
  - Events 1–3 appeared live.
  - Then the API was stopped, event 4 written while it was down, the API restarted, and event 5 written. All five show once, in order. The network log reads: dropped, 502, 502, 200.
  - Marking the operation succeeded turned the page into "rollback succeeded … Finished just now".
- **Logs with no worker:** the API's 503 shows as "logs are unavailable: the worker is not running".
- **Logs through the API with a stand-in worker:** a small Python server on `.dev/logs.sock` speaking ADR-0008's NDJSON (`E:\tmp\fake_logs.py`, outside the repository).
  - 5 tail lines plus one stderr line, then 3 live lines a second apart, then "The stream ended: the container stopped."
  - Follow off: the tail, then "end of the requested lines".
  - Stop mid-stream: no lines after it, and "Stopped."
- **Clean-up:** test token dev only and its file deleted; the API, the stand-in and the UI server stopped.
- **Not run:** logs from a real worker and container (covered for the API by the P2.7b tests and e2e).

**Next**
- P6.5: environment management (write-only values) and domains in the UI.

### 2026-10-06: P6.3b rollback in the UI

- **Phase / task:** P6.3b: rollback from a release; P6.3 is done
- **Author:** Claude Code (desktop session)

**Done**
- **`rollbackRules.ts`:**
  - `canRollBack`: only a superseded release, only with the deploy or admin scope.
  - `needsConfigChoice`: the API's 409 whose message names `with_current_config`.
  - `rollbackBody`.
- **`Rollback.tsx`:** confirms the target (release, commit, "nothing is rebuilt", the health gate), then queues it.
  - One `Idempotency-Key` per confirmation (`crypto.randomUUID()`), so a double click queues it once. A configuration choice is a different request, so it gets its own key.
  - After the 409 it offers "Today's values", "The values it ran with", or Cancel.
- **`AppDetail`:** a Roll back button per eligible release. A notice names the queued operation and says whether it replaced operations still waiting in the queue (`superseded`). The list then reloads.

**Decisions**
- **The 409 is recognized by its message,** which names the two fields, rather than by a problem `type`. The API sends `about:blank` everywhere. A test pins the API's exact sentence; giving the problem a type would be the sturdier fix, if the message ever changes.

**Verification**
- `tsc` exit 0; `node --test` 23 of 23 pass; the production build passes.
- **In the browser pane, against the real dev API** (data from P6.3a, plus a secret set, linked to the oldest release, then changed, all on the dev database):
  - With an admin token, 18 of 20 rows have the button: not the active or the failed one.
  - A plain rollback returned 202, and the notice named the operation.
  - The oldest release returned 409; the page offered the choice; "Today's values" returned 202.
  - The network log reads 202, 409, 202. In the database, the second operation's payload has `"with_current_config": true`; the first was cancelled because it was superseded, which the notice now says.
  - With a read token there are no buttons, and sign-in kept `/apps/web`.
  - Test tokens: 2 h, dev only, files deleted; the dev API and UI server stopped.

**Next**
- P6.4: live operation events and logs through `EventSource`. The client streams over `fetch`, since `EventSource` cannot send the token (P6.1b).

### 2026-10-06: P6.3a app pages

- **Phase / task:** P6.3a: the app list, app detail, and release history in the UI (P6.3 split: P6.3b is rollback)
- **Author:** Claude Code (desktop session)

**Done**
- **Routing without a library:**
  - `routes.ts` turns `/`, `/apps`, and `/apps/<slug>` (the API's slug rule) into pages and back.
  - `nav.tsx` has `navigate`, `useRoute` (links and the back button) and `Link` (a modified click is left to the browser).
  - Caddy's `index.html` fallback (P6.2b) makes these paths survive a reload.
- **`useApi`:** loads into a page and drops answers older than the newest request. Its messages turn a 404 into "Not found.", and an unreachable API into its own message.
- **Pages:**
  - `AppList`: slug, repository, branch, push deploys, age; an empty state that points to the CLI.
  - `AppDetail`: the settings, then the releases, 20 at a time with "Older releases" (the API's `before` cursor):
    - a status badge colored by tone, with the failure reason;
    - the commit (12 characters), the release ID, "rollback to …", the configuration revision, and the age.
  - An unknown app and an unknown path each get a message.
- **`format.ts`:** `ago`, `bytes`, `shortSHA`, `tone`, `statusLabel`.
- **Styles:** tables, badges, a settings grid. Wide tables scroll inside themselves at phone width, never the page.

**Decisions**
- **No router dependency:** three routes do not need one. **No new dependency** at all.

**Verification**
- `tsc` exit 0. `node --test`: 21 of 21 pass (routes, including rejected slugs and broken escapes; `ago`; `bytes`; statuses).
- **In the browser pane, against the real dev API** (127.0.0.1:18080). The data: two apps created through the API (201), and 25 deployments of one seeded by SQL on the dev database (1 active, 1 failed, 1 rollback, 22 superseded).
  - The list shows both apps; the link opens `/apps/web` without a reload.
  - The settings read correctly (`512 MiB`, `GET /healthz, 1m0s`).
  - The first page has 20 releases, newest first: the active one on top, the failed one with its reason, the rollback with its source.
  - "Older releases" brings 25, and the button goes.
  - Back returns to the list; a reload of `/apps/web` returns to it.
  - `/apps/nope` says there is no such app, and `/settings/x` says there is no page.
  - At 375 px the page does not scroll sideways; the table scrolls inside itself.
  - The browser pane was hidden, so it was driven through the DOM, not screenshots.
- **Not run:** Go checks (no Go changed).

**Next**
- P6.3b: rollback from a release.

### 2026-10-06: P6.2b web UI through Caddy

- **Phase / task:** P6.2b: Caddy serves the built UI on the API hostname; packaging; P6.2 is done
- **Author:** Claude Code (desktop session)

**Done**
- **`routing.Render`:** with `Settings.WebDir`, the API hostname serves the UI on every path but `/v1/*` and `/hooks/github`. The shape is taken from `caddy adapt` of a Caddyfile on 2.11.4:
  - headers: `WebCSP`, `X-Frame-Options`, `nosniff`, `Referrer-Policy`, COOP, and `Cache-Control` (`no-cache`, but `immutable` for `/assets/*`);
  - a `file` matcher with `try_files` and a `rewrite` to `index.html`, then `file_server`.
  - The directory is validated (absolute, clean, no `{}` placeholders), and is only allowed with an API hostname. New golden: `web`.
- **`runtime.EdgeSpec.WebDir`:** a read-only bind mount at the same path, validated. It is omitted from the hash when empty, so an edge without a UI is not recreated.
- **Config:** `SHIPYARD_WEB_DIR`. The default, `/usr/local/share/shipyard/web`, is used if it holds a UI; an explicit value must hold one; `off` disables it.
- **Worker:** `webUI` decides once at start, so the edge mount and the rendered route always agree. `restore` leaves the UI out, as it does the API socket.
- **`install.sh`:** copies `web/` (archive) or `web/dist` (checkout) in place, never replacing the directory, which would leave Caddy's mount stale. Order: directories (0755), then new assets, then `index.html` (0644), then stale files removed.
- **Release:** a GoReleaser `before` hook runs `make web-build`; setup-node in the release workflow; the archive ships `web/index.html` and `web/assets/*`.
- **Docs:** ADR-0016 note; env example; ARCHITECTURE; OPERATIONS (install, CLI-or-UI, upgrade); RELEASING; ACCEPTANCE (Node for snapshots, UI login in step 1); SOURCES `CADDY-JSON` and `GORELEASER-ARCHIVE`; ROADMAP; CHANGELOG.

**Verification**
- **Docker, against a real Caddy 2.11.4** (`TestRenderedConfigServes`, 50 s):
  - `/` and a deep link → `index.html` with all headers;
  - `/assets/app-abc123.js` → `immutable`;
  - `/v1/whoami` and `/hooks/github` → the API, without the UI's headers;
  - `/%2e%2e/%2e%2e/etc/passwd` → `index.html`, never outside the root;
  - POST, PUT and DELETE → error status.
  - The existing assertions without a UI still pass.
- **Edge tests** (`-tags docker`): `TestEdgeAdminGroup`, `TestEdgeBootstrap`, `TestEdgeJoinsAppNetworks`, `TestEdgeRefusesForeignContainer`: pass. Unit tests for routing, runtime, config and the worker (`TestWebUI`): pass. The existing golden files are unchanged.
- **`install.sh`'s web step, run for real in a scratch directory:**
  - The first install, then an upgrade: the new asset and new `index.html` arrive, and the old asset is removed.
  - The directory keeps its inode, so the bind mount stays valid. A rerun copies nothing. Without a build, it warns.
- **Release snapshot** (dist built on Windows; `--skip=before` in WSL, which has no Node):
  - The first attempt, with `web/dist/**/*`, flattened `assets/` and missed `index.html`; it was fixed with one-level globs.
  - After that: `web/index.html` plus `web/assets/*`, mode 0644, and `install.sh --dry-run` from the extracted archive installs the UI.
- **`make lint`, `go test -race ./...`:** exit 0.
- **e2e `TestPhase1ExitCriteria`:** the first run FAILED, the rerun on the same code PASSED (471.2 s), no leftovers.
  - The failure was at `deploy_test.go:222`: "the superseded container stopped before its observation window".
  - The last deploy finished at 16:36:36.8 on a very slow machine (an 8.4 s image export). The test inspects the old container only after `deploy --follow` returns, and the window is 6 s from activation, so the reconciler had already, correctly, stopped it.
  - The worker logged "no web UI" once, as expected; nothing in the UI change touches activation or the reconciler.
  - It is a timing race in the test, flagged as a separate task.

**Problems / surprises**
- **GoReleaser's `**` glob** under `dst` loses the directory layout `[GORELEASER-ARCHIVE]`. Caught only because the archive was inspected.
- **Local `make release-snapshot` now needs Node 24.** The owner's WSL has none (Windows does). Documented in RELEASING and ACCEPTANCE.
- **A flaky e2e assertion** (the observation window vs a slow `deploy --follow`), see Verification.

**Next**
- P6.3: app list, app detail, deployment history, and rollback in the UI.

### 2026-10-06: P6.2a web app and token login

- **Phase / task:** P6.2a: the React app, its build, and the token login (P6.2 split: P6.2b serves it from Caddy)
- **Author:** Claude Code (desktop session)

**Done**
- **ADR-0016** records the owner's choices (2026-10-06): esbuild, the token in `sessionStorage`, the UI on the API's hostname.
- **Dependencies** (exact): `react`, `react-dom` 19.3.0; dev: `esbuild` 0.28.2, `@types/react`, `@types/react-dom` 19.3.0.
- **`web/scripts/build.mjs`:**
  - Build: `dist/index.html` plus a hashed, minified `app-*.js` and `app-*.css`, with linked source maps and license file. No inline script or style.
  - `--dev`: watch, plus a same-origin server on 127.0.0.1:5173 that proxies `/v1` and the health routes to `SHIPYARD_API_URL` and streams SSE through.
- **App:**
  - `session.ts`: the token in `sessionStorage`, tolerating storage that throws; `signIn` through `whoami`, with messages for 401, 429 (`Retry-After`), other errors, and an unreachable API; `expiresIn`.
  - Components: `Login` (resumes a saved token after checking it), `Shell` (header and sign-out), `App`.
  - `Client` gains `onUnauthorized`: any 401 signs the tab out.
- **Make:** `web-check` now also builds; `make web-dev`.
- **Docs:** README (web), ROADMAP split, CHANGELOG, DEVELOPMENT, ARCHITECTURE layout, SOURCES `ESBUILD` and `NPM-SCRIPTS`.

**Decisions**
- **Build scripts in plain JavaScript** (`.mjs`), so there is no `@types/node`; `tsc` checks `src/`.
- **npm install scripts stay unapproved** (npm 11.16's new default). esbuild runs from its platform package.
- **No component tests yet** (a DOM library would be another dependency); the logic lives in `session.ts`, which is tested.

**Verification**
- **Web** (Windows, Node 24.18.0): `npm ci`, then `tsc` exit 0 and `node --test` 18 of 18 pass, then the build.
  - The build is 224.3 KB of JS and 1.5 KB of CSS, and two builds give the same hashes. `index.html` has no inline script or style.
- **In the browser pane,** against the real dev API (WSL, `127.0.0.1:18080`; 8080 is taken by another service on this machine):
  - A wrong token shows "The token is invalid, expired, or revoked." (a real 401 through the proxy).
  - A test token (2h, created on the dev database, file deleted after) signs in to "ui-check · admin · expires today".
  - A reload keeps the session; nothing is in `localStorage` or cookies.
  - Sign-out clears `sessionStorage`, and a reload stays on the login.
  - At 375 px there is no horizontal scroll, in dark and light schemes.
  - The dev server's SPA fallback serves a deep link; its proxy answers `/healthz`.
- **CI for P6.1b (`ts-client`, run 37462321598):** lint/unit, integration, and the new web job all succeeded.
- **Not run:** Go checks (no Go changed); P6.2b's serving.

**Next**
- P6.2b: Caddy serves `dist/` on the API hostname. This needs:
  - a read-only mount of a `SHIPYARD_WEB_DIR` and a file-server route in `routing.Render` (golden tests);
  - CSP and security headers, and an SPA fallback;
  - `install.sh` and the release workflow (Node in GoReleaser's `before` hooks, or a separate step).

### 2026-10-06: P6.1b TypeScript client

- **Phase / task:** P6.1b: the TypeScript types generated in Go, and the `fetch` client; P6.1 is done
- **Author:** Claude Code (desktop session)

**Done**
- **`internal/openapits`:** generates `web/src/api/schema.ts` from `api/openapi.json`:
  - an interface or alias per schema, in the spec's order, with descriptions as JSDoc;
  - an `Operations` map (request by location, the union of success bodies, a stream's events);
  - an `operations` constant (method, path, stream).
  - It refuses keywords outside the validated subset, unsupported `$ref`s, a stray `required`, and duplicate keys. `TestGenerated` fails on stale output; `make web-types` rewrites it.
- **`web/src/api/client.ts`:**
  - `Client.call` and `Client.stream`;
  - `ApiError` (problem details, `Retry-After`);
  - path values escaped, a missing path parameter refused.
  - A stream resumes with `Last-Event-ID` only when the server sent `retry`, so logs never replay their tail. It gives up after 5 drops in a row with no new event.
- **`web/src/api/sse.ts`:** an SSE parser over `fetch` per the HTML standard.
- **`web/`:** `package.json` (only `typescript` 7.0.2, exact, dev) and its lockfile, a strict `tsconfig.json`, `test/node.d.ts` (no `@types/node`), README.
- **Make and CI:** `make web-types`, `make web-check`, and a `web` CI job (`actions/setup-node@v7`, Node 24; the tag and its `node-version` input checked).
- Docs: ADR-0015 implementation note, SOURCES `NODE-TS` and `TS-7`, ARCHITECTURE, DEVELOPMENT, ROADMAP, CHANGELOG.

**Decisions**
- **No `@types/node`:** a 15-line declaration file types `node:test` and `node:assert/strict`.
- **`TextDecoder` with `stream: true`, not `TextDecoderStream`:** the latter does not type-check with `pipeThrough` in TypeScript 7.0.2 `[TS-7]`, and the former needs no cast.

**Verification**
- **Go:** `make lint` and `go test -race ./...`: exit 0. `TestSchemas`, `TestOperations`, `TestRefusesWhatItCannotType`, `TestGenerated`: pass. An edited `schema.ts` fails `TestGenerated` with "stale: run make web-types".
- **Web** (Windows, Node 24.18.0, npm 11.16.0): `tsc -p .` exit 0, so every `@ts-expect-error` line is a real error. With one of them removed, tsc fails with TS2554 (`getApp` without its path). `node --test`: 12 of 12 pass (calls, errors, path escaping, resume with `Last-Event-ID`, logs without resume, the parser's line endings, a character split across chunks, early exit cancelling the body).
- **Not run:** the `web` CI job itself (it runs on push). WSL has no Node; the web checks ran on Windows.

**Problems / surprises**
- **WSL's `npm` was Windows' npm through interop,** which cannot run from a WSL path. A first lockfile made that way was deleted, and the lockfile was made on Windows. It lists every platform's TypeScript binary, including `linux-x64` for CI.

**Next**
- P6.2: the React app in `web/` with a token login. That brings the decisions about a bundler and React's packages: ask the owner, since they are new dependencies.

### 2026-10-06: P6.1a OpenAPI description

- **Phase / task:** P6.1a: the OpenAPI description of `/v1` and its tests (P6.1 split: P6.1b is the TypeScript client)
- **Author:** Claude Code (desktop session)

**Done**
- **`api/openapi.json`** (OpenAPI 3.1, hand-written): the 21 routes but `/hooks/github`, with:
  - scopes as security-requirement role names;
  - request and response schemas, `additionalProperties: false` throughout;
  - the specific errors per route, and `default` for problem+json;
  - each event stream's data schemas (`x-events`).
- **`newMux`** now returns the registered routes and their scopes; `NewHandler` wraps it as before.
- **`internal/api/openapi_test.go`:**
  - `TestOpenAPIRoutes`: the same routes and scopes as `newMux`.
  - `TestOpenAPIWellFormed`: every `$ref` resolves, and operation IDs are unique.
  - `TestSchemaValidator`: the small validator's refusals.
  - **`conform`** wraps the handlers in the unit and integration fixtures. It checks each status, each response body (each SSE event's data), and each request body that succeeded.
- **`main_integration_test.go`:** a full integration run fails if a success response in the spec was never provoked.
- **Test fake fixed:** `fakeTokenAdmin.RotateToken` returned the old token without scopes, which the store cannot (CHECK). `conform` caught it as `"scopes": null`.
- ADR-0015; ARCHITECTURE (contract, layout); SOURCES `OAS-31`; README, CHANGELOG, ROADMAP.

**Decisions**
- **Owner's choices (2026-10-06):** hand-written JSON, checked by Go tests with no new dependency; for P6.1b, a TypeScript generator written in Go. The alternatives and why are in ADR-0015.
- **`/hooks/github` stays out of the spec:** GitHub's contract, authenticated by signature.

**Verification**
- `make lint` and `go test -race ./...`: exit 0. `TestOpenAPIRoutes`, `TestOpenAPIWellFormed`, `TestSchemaValidator`: pass.
- `go test -tags integration ./internal/api/` (PostgreSQL 18): ok (54.0 s). Every success response in the spec was provoked and conformed.
- **Drift check** (on a scratch copy): adding a required `owner` field to `App` in the spec failed `TestAppsCRUD` with `POST /v1/apps → 201: $: missing "owner"`.
- **Coverage:** the first integration run named the one success response no wrapped test provoked (`GET /v1/whoami 200`); wrapping the auth fixture covered it.

**Next**
- P6.1b: the Go generator for the TypeScript types and the `fetch` client in `web/`.

### 2026-10-06: P5.9a acceptance runbook

- **Phase / task:** P5.9a: the acceptance demo's runbook, sample app, and release rehearsal (P5.9 split: the run itself, P5.9b, needs the owner's VPSes)
- **Author:** Claude Code (desktop session)

**Done**
- **`docs/ACCEPTANCE.md`:**
  - what the run needs (two VPSes, two DNS names, a public repository with the sample app, an archive from `make release-snapshot` or an rc);
  - the six steps of ARCHITECTURE §9 with exact commands and pass criteria: webhook setup, HTTPS deploy, working and broken pushes, rollback, a `SIGKILL` mid-build, restore onto VPS B;
  - the checklist before `v1.0.0`, and a record template.
- **`examples/hello`:** a stdlib-only Go server (`/` prints `GREETING from VERSION`, `/healthz`) on `scratch` as uid 65534. Its `version` and `healthy` constants are the demo's working and broken changes. It is a separate module, so it stays out of `./...`.
- **Fix found by the rehearsal:** the server archive had no top-level directory, so `tar -xzf` spilled `deploy/` and the binaries into the current directory, and the operator guide's `shipyard-server_X.Y.Z_linux_amd64/deploy/install.sh` did not exist. Now `wrap_in_directory: true` `[GORELEASER-ARCHIVE]`.
- README, CHANGELOG, ROADMAP (P5.9 split into P5.9a and P5.9b).

**Decisions**
- **No tag, no rc, no GitHub repository created:** each is the owner's. The runbook recommends a local snapshot archive, which publishes nothing.

**Verification**
- **Sample app:** `go vet` and gofmt clean. Built with Docker and run with Shipyard's hardening (`--cap-drop ALL`, `no-new-privileges`, read-only, memory, pids and CPU limits, the `local` log driver):
  - good: `/` → `salaam from v1`, `/healthz` → 200;
  - broken (`healthy = false`): the build succeeds, `/healthz` → 503;
  - user `65534:65534`, and exit 0 on `SIGTERM` in both.
- `make lint`: exit 0. `make release-check`: config valid.
- **`make release-snapshot`:** exit 0, before and after the fix. After it, every file is under `shipyard-server_0.1.1-next_linux_amd64/` with modes kept (`install.sh`, the firewall script and the binaries 0755). `install.sh --dry-run` from the extracted directory installs `../shipyard-api` and `../shipyard-worker`.
- **Not run:** the demo itself (P5.9b).

**Next**
- The owner: the run in ACCEPTANCE.md on two fresh VPSes, recorded there. Then merge the stack to `main`, and tag `v1.0.0` per RELEASING.md.

### 2026-10-06: P5.8b Caddy admin group

- **Phase / task:** P5.8b: a group of its own for the Caddy admin socket (security review F2); P5.8 is done
- **Author:** Claude Code (desktop session)

**Done**
- **Config:** `SHIPYARD_CADDY_GROUP` (default `shipyard-caddy`, validated as a group name). Set explicitly, the group must exist.
- **Worker:** `adminGroup` looks the group up.
  - If it is missing and was not set explicitly, the worker uses its own group, as before, and warns.
  - If the worker is not in it, the worker refuses to start the edge.
  - `edgeSpec` gives Caddy that gid, plus the worker's own group as `APIGID` when there is an API hostname (the API's socket).
  - `restore` uses the same group.
- **Runtime:** `EdgeSpec.APIGID` (omitted from the spec hash's JSON when 0) becomes `HostConfig.GroupAdd`, unless it equals `GID`.
- **`install.sh`:** `groupadd --system shipyard-caddy` and `usermod -aG shipyard-caddy shipyard-worker`, both idempotent.
- **Docs:**
  - ADR-0003 note;
  - ARCHITECTURE (process table, admin socket, user);
  - the security review (F2 fixed, invariants 1 and 12);
  - OPERATIONS (upgrade recreates Caddy once), CHANGELOG, env example;
  - SOURCES `SYSTEMD-EXEC` re-read.

**Decisions**
- **Membership through the user database, not `SupplementaryGroups=`.** With `User=`, systemd takes the user's groups from the database and that option only extends them `[SYSTEMD-EXEC]`. A unit listing a missing group would fail to start; this way, a host where `install.sh` was not rerun still starts, with a warning. The `systemd-run --uid=shipyard-worker` commands in RESTORE and OPERATIONS get the group the same way, unchanged.
- **No new ADR:** the decision in ADR-0003 (a permissioned Unix socket) is unchanged; only who holds the permission changed.

**Verification**
- `make lint`: exit 0 after a gofmt fix. `go test -race ./...`: exit 0.
- `TestAdminGroup`, `TestEdgeSpecGroups`, `TestLoadWorkerCaddy`, `TestEdgeSpecValidate`, `TestEdgeSpecHash`: pass.
- **Docker** (`-tags docker -run TestEdge ./internal/runtime/`), all pass:
  - `TestEdgeAdminGroup` (11.98 s): a real container shows the directory's group and mode 2770, the socket's group and mode 0660, user `0:<gid>`, `GroupAdd`, and both groups in `id -G`.
  - `TestEdgeBootstrap`, `TestEdgeJoinsAppNetworks`, `TestEdgeRefusesForeignContainer`.
- `install.sh`: `bash -n` ok. The dry run shows the `groupadd` and `usermod` steps.
- **e2e** `TestPhase1ExitCriteria`: PASS (911.7 s, slower than the usual ~390 s after a WSL restart with cold caches). No leftover containers. The worker ran without the group, so it took the fallback path.
- **Not run:** a real `install.sh` on a server; `TestRestoreDrill`, `TestCrashSafety`.

**Next**
- P5.9 (the owner's): the acceptance demo on a fresh VPS, then the `v1.0.0` tag on the owner's request.

### 2026-10-05: P5.8a security review

- **Phase / task:** P5.8a: security review against the invariants, govulncheck, dependency audit (P5.8 split: P5.8b separates the Caddy admin socket's group)
- **Author:** Claude Code (desktop session)

**Done**
- **`docs/security-review.md`:** 8 findings, the 12 invariants each traced to the code that enforces them and the test that proves them, the Phase 5 surfaces, accepted risks, and the dependency audit.
- **Fixes:**
  - **F1:** `golang.org/x/text` v0.29.0 → v0.42.0 (GO-2026-5970, reachable via pgx); `x/sync` → v0.23.0.
  - **F3:** `github.LoadKey` refuses group-readable keys (the API shares the group). Docs say owner `shipyard-worker`, 0600.
  - **F4:** `install.sh` passes the env values to `awk` through its environment, not argv.
  - **F5:** the build-check log name comes from `mktemp`.
  - **F7:** `TestAPIDependencies` pins invariant 1 to `shipyard-api`'s import graph.
  - **F8:** `make vuln` (govulncheck v1.8.0, both tag sets) and a CI step.
- **F2** (the Caddy admin socket reachable by the API's group): the owner chose to fix it now, as P5.8b.
- **F6** (apt key fingerprints): accepted; no vendor publishes one to pin.

**Decisions**
- **Available non-security updates are deferred** (moby client 0.6.1 and api 1.56.1, x/sys, otel): they belong in a routine update with the Docker tests.
- **CLAUDE.md is not edited** (the owner's file); `make vuln` is documented in the Makefile help and here.

**Verification**
- **Before the fix,** `make vuln` reported GO-2026-5970 with traces through `pgx.Connect` → `norm.Form.*`, exit 3. **After it:** "No vulnerabilities found" for both tag sets.
- `go mod verify`: all modules verified.
- `make lint` and `go test -race ./...`: exit 0. `TestAPIDependencies` and `TestLoadKey` (with the new group-readable case) pass.
- **Licenses:** read from each linked module's license file. 24 modules, all Apache-2.0, MIT or BSD-3-Clause, and none ships a NOTICE.
- **`install.sh`:** `bash -n` ok and dry run ok. The awk rewrite changes exactly the 4 lines, keeps the line count, and leaves no literal password in the script.
- **Integration** (pgx with the new `x/text`): `internal/store`, `internal/api`, `cmd/shipyard-api`, all ok.
- **Not run:** e2e (P5.8b runs it after the Caddy change), `TestCrashSafety`.

**Next**
- P5.8b: the `shipyard-caddy` group for the admin socket.

### 2026-10-05: P5.7c operator guide

- **Phase / task:** P5.7c: operator guide; P5.7 is done
- **Author:** Claude Code (desktop session)

**Done**
- **`docs/OPERATIONS.md`:**
  - requirements;
  - install (verify, dry run, flags, what the installer does, KEK copy, ufw, CLI login, first app);
  - configuration, and the `systemd-run` form for worker commands;
  - upgrade: backup, installer, live-restore's patch-only limit `[DK-LIVE]`, the Caddy-recreate exception, PostgreSQL majors;
  - backup and restore, keys and tokens, monitoring;
  - troubleshooting (9 symptoms);
  - uninstall (by hand, after a backup).
- **`install.sh --for-restore`:** packages, users, files, `shipyard.env` and an **empty** database. No KEK, no migrations, no services. It then prints the remaining RESTORE.md steps.
- **`docs/RESTORE.md`:** now uses the installer (step 1 `--for-restore`, step 6 a plain rerun) and HPKE key files (`.hpke` 0600 owned by the worker, `.pub`).
- **README** (install, docs table), RELEASING and `.goreleaser.yaml`: the server archive ships `docs/OPERATIONS.md` and `docs/RESTORE.md`.

**Decisions**
- **No uninstall script:** removal destroys data, so the guide lists what to remove by hand.
- **Debian is accepted but marked untested:** only Docker's Ubuntu page was verified.

**Verification**
- `bash -n`: ok. `--help` shows `--for-restore`.
- **`--dry-run --for-restore`:** it ends after the env file and the database, and plans no `kek generate`, `migrate` or service start (count 0).
- `make release-check`: validated.
- Claims in the guide were checked against the code:
  - the sysctl key;
  - the worker's start error text;
  - the warnings' wording;
  - CLI flags;
  - the Caddy spec-hash recreate.
  - A wrong row about delete order was removed: routes leave first.
- **Not run:** any of the guide's host commands on a real server (P5.9).

**Next**
- P5.8: security review against the invariants, `govulncheck`, a dependency audit.
- Owner: a real `install.sh` on a fresh VPS, then the acceptance demo (P5.9).

### 2026-10-05: P5.7b installer

- **Phase / task:** P5.7b: `deploy/install.sh`
- **Author:** Claude Code (desktop session)

**Done**
- **`deploy/install.sh [--bin DIR] [--public-ip IP]… [--api-hostname H] [--acme-email E] [--skip-build-check] [--dry-run]`,** for Ubuntu or Debian, amd64 or arm64, as root:
  - **Shipyard's binaries:** from the release directory or `./bin`; it never downloads Shipyard.
  - **Dependencies (the owner's choice):** Docker Engine (with buildx) and PostgreSQL 18, from their vendors' deb822 apt sources, when missing `[DK-INSTALL][PG-APT]`. An existing Docker older than 29 gets a warning. `daemon.json` is installed only when absent; a different one is reported, never overwritten.
  - **Accounts and files:**
    - users `shipyard-api` and `shipyard-worker` (also in `docker`), with homes under `/var/lib`, and group `shipyard`;
    - directories with their modes;
    - binaries, units, the firewall script; on Ubuntu ≥ 24.04, the userns sysctl.
  - **First install only:**
    - `shipyard.env` from the example, with a random database password and the flags;
    - the role and database, with the SQL on stdin, idempotent;
    - an HPKE KEK `k1` (private key owned by the worker);
    - after the services start, the first admin token, printed once.
  - **Every run:** `shipyard-api migrate` as `shipyard-api`, enabling and (re)starting the services, then a rootless build check (busybox `RUN`) as the worker's user.
- **Worker unit fix:** it was never run on a host, and `ProtectSystem=strict` left `/var/lib/shipyard/work` and `/run/shipyard` read-only. Now:
  - `ReadWritePaths=-/var/lib/shipyard/work`;
  - `RuntimeDirectory=shipyard-worker shipyard`;
  - `RuntimeDirectoryPreserve=yes`, since Caddy bind-mounts the admin directory.
- **`.gitattributes`:** `*.sh` stays LF. **`.goreleaser.yaml`:** ships `deploy/install.sh` (0755).
- **Docs:** deploy README (no longer "skeleton"), SOURCES (`DK-INSTALL`, `PG-APT`, `SYSTEMD-EXEC` re-read), ROADMAP, CHANGELOG.

**Decisions**
- **The KEK ID is `k1`, and the env example's value is kept.**
- **The admin token is printed once, never stored.**
- **An upgrade migrates before it restarts the services.** The old binaries run briefly against the new schema; migrations are forward-only and additive. OPERATIONS.md (P5.7c) says to back up first.

**Verification**
- `bash -n`: ok. `--help`, an unknown option, and a non-root run without `--dry-run` (refused) behave as expected.
- **`--dry-run` as a normal user on WSL Ubuntu 24.04** (Docker present, no host PostgreSQL): it plans every step in order (daemon.json, PGDG and `postgresql-18`, users, directories, files, the sysctl, the firewall, env and database, KEK, migrate, services) and changes nothing.
- **The env rewrite on a scratch copy:** exactly the database URL, public IPs and API hostname change. The file loads with `set -a; .`; `shipyard-api` runs with it.
- **The role and database SQL, twice against the dev PostgreSQL 18** (renamed `sy_install_test`): both runs ok, the database is owned by the role; then dropped.
- **Not run:** a real install (needs root on a fresh host). That is the owner's step on a VPS or a throwaway VM: `sudo deploy/install.sh --public-ip <IP>`. shellcheck is not installed.

**Next**
- P5.7c: `docs/OPERATIONS.md`.

### 2026-10-05: P5.7a build network firewall

- **Phase / task:** P5.7a: host firewall for the build network (ADR-0009). P5.7 is split into a (firewall), b (`install.sh`), and c (operator guide).
- **Author:** Claude Code (desktop session)

**Done**
- **Owner's choices** (2026-10-05):
  - the installer installs Docker Engine and PostgreSQL 18 from their apt repositories when missing (P5.7b);
  - the firewall matches a fixed bridge name, not a subnet.
- **`internal/build`:** the build network gets `com.docker.network.bridge.name=sybuild-<7 hex of sha256(builder)>` (`Builder.Bridge`). A network without it is removed, with its builder, and recreated.
- **`deploy/firewall/shipyard-firewall.sh apply|remove|status`:**
  - **To the host:** `SHIPYARD-BUILD-IN` is jumped to first from INPUT for `-i sybuild-+`. It drops all traffic to the host but replies.
  - **Forwarded:** `SHIPYARD-BUILD-FWD` is jumped to from DOCKER-USER. It drops `169.254.0.0/16` (v6: `fd00:ec2::254`).
  - **Behavior:** idempotent. It refuses Docker's nftables backend and a missing DOCKER-USER chain.
- **`deploy/systemd/shipyard-firewall.service`:** a oneshot after and part of `docker.service`. The worker unit wants it and starts after it.
- **Docs:** ADR-0009 ("As implemented"), SOURCES (`DK-BRIDGE` observation, `DK-IPTABLES`, `DK-NFTABLES`, `NF-CHAINS`), ARCHITECTURE, ROADMAP (split), CHANGELOG, deploy README.
- **Release archive** (`.goreleaser.yaml`, RELEASING.md): `shipyard-server` now also ships `deploy/sysctl`, `deploy/prometheus` (both missed before) and `deploy/firewall` (mode 0755). `make release-check`: "1 configuration file(s) validated".

**Decisions**
- **All host services are blocked from builds,** on every address, rather than an allow-list: builds need none (DNS goes through Docker's embedded resolver, inside the container's namespace).
- **Other private ranges (a provider's VPC) stay reachable;** ADR-0009 asks only for metadata and the host.

**Verification**
- `make lint` and `go test -race ./...`: exit 0. `bash -n` on the script: ok.
- **Docker tests (WSL, Engine 29.8.1), PASS:**
  - `TestBuilderNetwork`: the bridge option, and the interface exists on the host;
  - `TestBuilderReplacesUnnamedNetwork`: first run failed (`buildx rm` of a missing builder says "no builder … found"); fixed by removing only an existing builder; rerun passed;
  - `TestBuilderRootless`, `TestBuilderReplacesOtherImage`;
  - `TestFirewallScript`: the script as root in `unshare -rn`, with no real firewall touched. Rules after two applies are one each (v4 and v6); remove leaves only DOCKER-USER; nftables backend and a missing DOCKER-USER are refused.
- **Unit:** `TestBridge`.
- `go test -tags e2e -run TestPhase1ExitCriteria`: PASS, 382.9 s; no leftovers (builds ran on the named bridge).
- **Not run:** the script against a real host firewall (needs root): the owner's step, `sudo deploy/firewall/shipyard-firewall.sh apply && sudo deploy/firewall/shipyard-firewall.sh status`. shellcheck is not installed in WSL.

**Next**
- P5.7b: `deploy/install.sh`.

### 2026-10-05: P5.6 disk and certificate checks

- **Phase / task:** P5.6: disk usage and certificate expiry metrics and alerts at 80% (ADR-0014)
- **Author:** Claude Code (desktop session)

**Done**
- **Owner's choice** (2026-10-05): alerts are log warnings plus a shipped Prometheus rules file, with no webhook.
- **`internal/monitor`:** a `Checker`, run by the worker at start and every `SHIPYARD_CHECK_INTERVAL` (5m).
  - **Disks:** `statfs` of Docker's data root (`runtime.DockerRootDir`), the work directory, and the backup directory. A missing path is measured at its parent.
  - **Certificates:** a TLS handshake (SNI, dates only) to Caddy's published 443 (`runtime.EdgeHTTPSAddr`) for every route hostname and the API hostname.
  - **Logging:** a warning when a disk or a certificate's lifetime passes 80%, or no certificate is presented; an info line when it ends. Once per change.
- **Metrics:** `shipyard_filesystem_{size_bytes,avail_bytes,used_ratio}`, `shipyard_certificate_{ok,not_after_timestamp_seconds,lifetime_used_ratio}`, `shipyard_checks_timestamp_seconds`.
- **`deploy/prometheus/shipyard-alerts.yml`:** 8 rules (disk, certificate renewal, certificate missing, stale checks, app unhealthy, database down, deploys failing, stuck queue).
- **Docs:** ADR-0014, SOURCES (`CM-RENEW`, `PROM-RULES`, `PROM-TEMPLATE`), ARCHITECTURE, ROADMAP, CHANGELOG, env example, deploy README.

**Decisions**
- **Served, not stored:** certificates are read through a handshake, not from Caddy's storage.
- **No `docker system df`:** the data root's filesystem covers the same bytes and is fast.
- **An unreadable certificate counts as bad,** like an expiring one.

**Verification**
- `make lint` and `go test -race ./...`: exit 0.
- **New unit tests:**
  - `TestRatios`;
  - `TestCheckerWarnsOnCrossing`: disk fills and frees, an old certificate, a missing one, a removed then re-added hostname, one warning per change;
  - `TestCheckerDisksOnly`, `TestSorted`;
  - `TestStatFS`: a real path and a not-yet-created one;
  - `TestServedCert`: the test TLS server's dates; a plain HTTP port fails;
  - `TestWorkerMetricsChecks`, `TestLoadWorkerCheckInterval`;
  - `TestAlertRulesUseRealMetrics`: every `shipyard_*` name in the rules file is exposed by the worker or the API.
- `go test -tags e2e -run TestPhase1ExitCriteria`: PASS, 396.7 s; no leftovers. It waited for `shipyard_certificate_ok` = 1 and a lifetime ratio for the app hostname (Caddy's internal CA), and for the disk series.
- **Not run:** `promtool check rules` (needs the Prometheus image); `TestRestoreDrill`, `TestCrashSafety`.

**Next**
- Owner: `promtool check rules deploy/prometheus/shipyard-alerts.yml` if Prometheus is at hand.
- P5.7: `deploy/install.sh` and the operator guide.

### 2026-10-05: P5.5b app health and API metrics

- **Phase / task:** P5.5b: each active app's health, and the API's metrics listener (ADR-0013); P5.5 is done
- **Author:** Claude Code (desktop session)

**Done**
- **Reconciler:** after the restore step, each pass reports every active app: its container running, and one GET of its health path at the container IP (`health.Probe`, the gate's rules, 2 s). A recreated container is checked under its new ID. The step runs only when the worker's metrics are on.
- **Worker metrics:** `shipyard_app_up{app}`, `shipyard_app_healthy{app}` (replaced as a whole, so deleted apps drop out), and `shipyard_app_health_checked_timestamp_seconds`.
- **API:** `SHIPYARD_API_METRICS_LISTEN` (loopback, different from the worker's) serves `shipyard_api_requests_total{code}` by status class, `shipyard_api_throttled_total{reason}` (`requests` or `auth`), and `shipyard_build_info`. No label names a client, a token or a path.
- **`internal/metrics`:** `Gauge.Replace`, `Serve`, `BuildInfo`.
- **Docs:** ADR-0013 (P5.5b decisions), ARCHITECTURE, ROADMAP (P5.5 ticked), CHANGELOG, env example.

**Decisions**
- **Health is information only:** a failed probe restarts nothing and changes no route. Probing happens once per reconcile pass, not at scrape time (ADR-0013 alternatives).

**Verification**
- `make lint` and `go test -race ./...`: exit 0.
- **New unit tests:**
  - `TestPassReportsHealth`: running and healthy, running but unhealthy, recreated, stopped; no report when listing fails; an empty list when there are none;
  - `TestProbe`, `TestGaugeReplace`, `TestWorkerMetricsHealth`;
  - `TestAPIMetrics` (200, 429 by rate, 401, 429 by auth, healthz) and `TestAPIMetricsOff`;
  - `TestLoadWorkerMetricsListen`, extended to the API and the same-port check.
- `go test -tags e2e -run TestPhase1ExitCriteria`: PASS, 404.1 s; no leftovers. It now also waits for `shipyard_app_up` and `shipyard_app_healthy` of the app to be 1, and reads a non-zero API 2xx count.
- **Not run:** `TestRestoreDrill` and `TestCrashSafety`.

**Next**
- P5.6: disk usage and certificate expiry metrics and alerts.

### 2026-10-05: P5.5a worker metrics

- **Phase / task:** P5.5a: Prometheus metrics on an internal listener, worker part (P5.5 split: P5.5b adds app health and the API's 429s)
- **Author:** Claude Code (desktop session)

**Done**
- **`internal/metrics`:** counters, gauges, and histograms with fixed labels, written in the text format 0.0.4 `[PROM-TEXT]`. `GET /metrics` only. No dependency.
- **Worker:** `SHIPYARD_WORKER_METRICS_LISTEN` takes a loopback `host:port` and is off by default. Each scrape reads PostgreSQL with a 5 s limit:
  - `shipyard_operations_total{kind,result}` and `shipyard_operation_duration_seconds{kind,result}`: a cursor on `finished_at`, starting at the newest finish when the worker starts, re-reads one minute back so a late commit is still counted, and dedupes by ID;
  - `shipyard_queue_operations{status}` and `shipyard_queue_oldest_wait_seconds`;
  - `shipyard_database_up` (on failure: 0, and the queue gauges are dropped);
  - `shipyard_build_info`.
- **Store:** `QueueStats`, `FinishedOperations`, `LastFinished`.
- **Docs:** ADR-0013, SOURCES (`PROM-TEXT`, `PROM-PORTS`), ARCHITECTURE, ROADMAP (P5.5 split), CHANGELOG, env example.

**Decisions**
- **Conventional options, chosen without asking (CLAUDE.md §10):** no client library; metrics read from the database rather than counted on each code path; loopback only; no default port. ADR-0013 lists the alternatives for the owner to revisit.

**Verification**
- `make lint` and `go test -race ./...`: exit 0.
- **New unit tests:**
  - `TestExposition`, `TestGaugeResetAndScrape`, `TestFormatFloat`, `TestRegistryRejects` (8 cases), `TestHandler`;
  - `TestWorkerMetrics`: nothing from before the start, a late commit counted, no double count, database down;
  - `TestWorkerMetricsPages`: 2005 operations over three pages, counted once;
  - `TestLoadWorkerMetricsListen`.
- **Integration:** `TestQueueStatsAndFinished`: ok. The first attempt timed out creating its database while the dev PostgreSQL was restarting; the rerun passed.
- `go test -tags e2e -run TestPhase1ExitCriteria`: PASS, 391.5 s; no leftovers. It now scrapes the worker after the first deploy: `shipyard_operations_total{kind="deploy",result="succeeded"} 1` and `shipyard_database_up 1`.
- **Not run:** `TestRestoreDrill` and `TestCrashSafety`; the restarted workers there reuse the same metrics port.

**Next**
- P5.5b: app health from the reconciler, and the API's metrics listener.

### 2026-10-04: P5.4b HPKE KEKs

- **Phase / task:** P5.4b: asymmetric sealing so the API cannot decrypt (ADR-0012)
- **Author:** Claude Code (desktop session)

**Done**
- **`internal/secrets`:** a KEK is now an interface with two kinds.
  - Symmetric `<id>.key` (as before).
  - HPKE `<id>.hpke` plus `<id>.pub`: `crypto/hpke`, DHKEM(X25519, HKDF-SHA256) / HKDF-SHA256 / AES-256-GCM, info `shipyard-dek-v1|kek|value`.
  - **`LoadKeyring`** (worker): every kind; a `.hpke` must be mode `0600`-like (no group or other bits); a `.pub` must match its private key; an ID defined twice is an error.
  - **`LoadSealKeyring`** (API): the active KEK only, `.key` or else `.pub`, never a private key.
  - **New functions:** `CanOpen`, `GenerateHPKE`.
- **API:** `shipyard-api serve` uses `LoadSealKeyring`.
- **Worker:** refuses to start if it cannot open the active KEK. `kek rewrap` precheck and `kek status` tell "public key only" apart from "not loaded".
- **`shipyard-worker kek generate ID`:**
  - writes the pair into `SHIPYARD_KEK_DIR` without needing the database;
  - creates both files exclusively, leaves no half pair, and refuses an ID that has any key file;
  - prints the next steps.
- **Backups:** target B copies `.hpke` and `.pub` files, and the manifest lists each KEK ID once. A restore accepts a `.key` or a `.hpke` per ID.
- **Docs:** ARCHITECTURE (component table, secrets), env example, ROADMAP (P5.4 ticked, P5.7 item), CHANGELOG.

**Decisions**
- **DHKEM(X25519), not X-Wing:** final in RFC 9180, versus a draft; ADR-0012.
- **The private-key mode check is stricter than for `.key`:** the API shares the `shipyard` group, so group read must be refused.
- **`kek generate` writes into the KEK directory directly.** The operator runs it as root and chowns the `.hpke` to the worker; the printed steps say so.

**Verification**
- `make lint` and `go test -race ./...`: exit 0.
- Integration for `cmd/shipyard-worker`, `cmd/shipyard-api`, `internal/secrets`, `internal/backup`: ok.
- **New tests:**
  - `TestHPKEKeyring`: the API keyring seals and cannot open; the worker opens; another row, a relabel, or a tampered wrapping each give `ErrDecrypt`; symmetric→HPKE rewrap, after which `.key` and other keys can go.
  - `TestLoadHPKERejects` (6 cases) and `TestLoadSealKeyring` (never reads `.hpke`, loads only the active KEK).
  - `TestKEKGenerate`, `TestKEKRotationToHPKE` (the API sets with the public key; `Resolve` fails for the API and succeeds for the worker).
  - Backup and restore cases for `.hpke` and `.pub`.
- `go test -tags e2e -run TestPhase1ExitCriteria`: PASS, 558.3 s; no leftovers.
- **Not run:** `TestRestoreDrill` and `TestCrashSafety`; the session's usage limit was reached.

**Next**
- Owner: run `make test-e2e`; `TestRestoreDrill` covers the backup of KEK files.
- P5.5: Prometheus metrics on an internal listener.

### 2026-10-04: P5.4a KEK rotation

- **Phase / task:** P5.4a: the KEK rotation command with a test (P5.4 split: P5.4b implements asymmetric sealing)
- **Author:** Claude Code (desktop session)

**Done**
- **Owner's choices** (2026-10-04): allow a re-wrap-only update of `secret_values` rather than a side table; implement asymmetric sealing as P5.4b instead of only evaluating it.
- **[ADR-0012](adr/0012-kek-rotation-and-asymmetric-sealing.md):** the rotation design and the HPKE design (`crypto/hpke`, DHKEM X25519, not the X-Wing draft).
- **Migration `0005_secret_rewrap.sql`:** the `secret_values` trigger now allows only a new `wrapped_dek` with a new `kek_id`; anything else is still `SY001`.
- **Store:** `KEKUsage`, `WrapsNotUnder` (keyset by ID), `Rewrap` (conditional on the old `kek_id`).
- **Secrets:** `Keyring.Rewrap` (open the DEK with its KEK, seal it with the active one; the ciphertext is untouched), `Active`, `Has`, `IDs`.
- **`shipyard-worker kek status|rewrap`:**
  - refuses to start while a KEK in use is not loaded;
  - stops at the first value that does not open;
  - is safe to run again;
  - writes the audit event `kek.rewrap`.
- **Docs:** ADR-0005 note, ARCHITECTURE §7 secrets, SOURCES (`GO-HPKE`, `RFC9180`), ROADMAP split, CHANGELOG.

**Decisions**
- **The re-wrap must change both columns.** A new `kek_id` alone (a relabel) is still rejected, as the existing schema test expects.
- **The worker runs rotation,** because it is the process that may open data keys. After P5.4b the API will not be able to.
- **One pass in ID order.** Values that an API still on the old KEK seals during the pass show up in `kek status`, and a second run moves them.

**Verification**
- **First full run:**
  - `make test` passed.
  - `make lint` needed gofmt on `envelope_test.go`.
  - `make test-integration` was cut off by this session's 10-minute run limit while `internal/store` was still running (load average about 5). Before that, every other package passed except `TestLeaseHandover`, the known flake. That includes `cmd/shipyard-worker` with `TestKEKRotation`, `internal/secrets`, and `internal/api`.
- **`internal/store` alone:** finished in 203 s, so nothing hangs. Only `TestAudit` failed: the event recorded first had the later `now()`. That fits the WSL clock jumps this session ("Time jumped backwards" in the journal); the test is untouched.
- **After the gofmt fix:**
  - `make lint`: exit 0.
  - `-count=3` of `TestAudit|TestSchema*|TestTokens|TestRotateToken`: ok.
  - `-count=3` of `TestLeaseHandover`: ok.
  - `TestKEKRotation`: PASS.
- **New tests:**
  - `TestKEKRotation` (integration):
    - status before;
    - 3 values moved from k1 to k2 while a value already under k2 stays;
    - values open with k2 alone; the revision's entries are unchanged;
    - status "unused: may be retired"; a second run moves 0; audit `0 values to k2`;
    - a value under unloaded k3 blocks the run, and status says NOT LOADED;
    - a corrupt wrapping stops it with "cannot be decrypted".
  - `TestRewrap` (unit): the ciphertext is unchanged, opens with the new KEK alone; unknown KEK, wrong KEK, other value ID, tampered input, and a relabel each give `ErrDecrypt`.
  - `TestSchemaSecretsAndAudit`: a relabel, a new wrapping without a new KEK, a ciphertext change, and a key change are rejected; a proper re-wrap is accepted.
  - `TestRun`: `kek` argument checks.

**Next**
- P5.4b: HPKE KEKs (`<id>.hpke`/`<id>.pub`), `kek generate`, seal-only keyring in the API, backups of the new files.

### 2026-10-04: P5.3b token rotation

- **Phase / task:** P5.3b: token rotate on the server, and token list, revoke, and rotate through the API and CLI (ADR-0011)
- **Author:** Claude Code (desktop session)

**Done**
- **Store:**
  - `TokenByPrefix`.
  - `RotateToken(oldID, new, oldEnds)`: in one transaction it ends the old token (revoked when `oldEnds` is not in the future, otherwise `expires_at = least(expires_at, oldEnds)`) and inserts the new one. A token that is no longer active gives `ErrNotFound` and changes nothing.
- **`api.Rotation`:** the old token's user, name, and scopes, and its lifetime (creation to expiry, 90 days if it has none, clamped to 1h–366d) counted from now.
- **Shared lifetimes:** `MinTokenTTL`, `MaxTokenTTL`, `DefaultTokenTTL`, `MaxRotationGrace` (7 days) are now exported and used by `shipyard-api`.
- **`shipyard-api token rotate PREFIX [--grace D]`:** prints the new token on stdout and only there; audit `token.rotate` with target `old -> new`.
- **API:**
  - `GET /v1/tokens` and `DELETE /v1/tokens/{prefix}` (admin);
  - `POST /v1/tokens/self/rotate` (any scope, optional `grace_seconds`): 201 with `Cache-Control: no-store`; 409 if the token went inactive meanwhile.
- **Client:** `ListTokens`, `RevokeToken`, `RotateSelf`.
- **CLI:** `shipyard token list|revoke|rotate`.
  - `rotate` saves the new token to the config file (read without the environment overrides, so `SHIPYARD_URL` is not persisted).
  - With `SHIPYARD_TOKEN` set it prints the token instead. If saving fails it prints it too, so the only copy is never lost.
- **Docs:** ARCHITECTURE (access, CLI table), ROADMAP (P5.3 ticked), CHANGELOG.

**Decisions**
- **Any scope may rotate itself:** the new token has the same scopes, so nothing escalates. It does renew the lifetime; that is accepted in ADR-0011.
- **The CLI `--grace` takes Go durations** (`24h`, `168h`); the server checks the 7-day bound and answers 422.

**Verification**
- First run:
  - `make test` passed.
  - `make lint` needed gofmt on two files.
  - `TestTokenCommand` **caught a bug**: a rotation without `--grace` left the old token working. See Problems.
  - `TestLeaseHandover` failed: the known flake, which passed in the rerun below.
- After the fix:
  - `make lint` and `go test -race ./...`: exit 0.
  - Integration for `cmd/shipyard-api`, `cmd/shipyard`, `internal/api`, `internal/queue`: ok.
  - `internal/store`: everything passed but `TestCreateRollbackDeployment` ("unbuilt source: invalid value"), which this change does not touch; alone, `-count=5` passes.
- **New tests:**
  - store `TestRotateToken`: no grace revokes; grace keeps the old token about 1h; the old expiry wins over a longer grace; a token without expiry gets one; a revoked token gives `ErrNotFound` and inserts nothing; `TokenByPrefix`.
  - api:
    - `TestTokenRoutesNeedAdmin`: 403 for deploy; no hash in the list; 404 for unknown and malformed prefixes.
    - `TestRotateSelf`: a read token rotates itself; `no-store`; the new hash matches the returned token; 90-day default lifetime; grace passed through; 422/400/409/401 cases.
    - `TestRotationKeepsLifetime`: clamped to 1h–366d.
  - `cmd/shipyard-api`: `TestParseTokenRotate` and the rotate part of `TestTokenCommand` (lifetime kept, old revoked, audit `old -> new`, `--grace`, revoked and unknown refused).
  - `cmd/shipyard`: `TestCLITokens`:
    - list; rotate saves the new token and never prints it; the old one stops working;
    - with `SHIPYARD_TOKEN` it prints the token and leaves the file alone;
    - list with a read token gets 403; unknown revoke gets 404; revoke shows `revoked`.
- e2e: not run. The deploy path is unchanged since P5.3a's e2e passed with the limiter on.

**Problems / surprises**
- **The rotation bug.** At first `RotateToken` took an absolute end time and compared it with PostgreSQL's `now()`. That is the transaction's start time, which comes before the `time.Now()` the command took inside the transaction, so "end now" looked like a short grace. It now takes the grace as a duration, and the database computes the end (`$2::bigint` microseconds).
- **Flaky store tests** under the full parallel run: `TestCreateRollbackDeployment` here, `TestLeaseHandover` earlier; both pass alone.
- **A heredoc slipped into the repo.** I appended `TestCLITokens` to `cmd/shipyard/cli_integration_test.go` with a shell heredoc, which CLAUDE.md §2.4 forbids. The content had no nested `EOF` and came out right; later edits use the editor tools only.

**Next**
- P5.4: KEK rotation command with a test, and an evaluation of asymmetric sealing.

### 2026-10-04: P5.3a API limits

- **Phase / task:** P5.3a: per-client API request limits and auth-failure throttling (P5.3 split in two: over 400 lines with the token commands)
- **Author:** Claude Code (desktop session)

**Done**
- **Owner's choices** (2026-10-04): the proposed defaults (10 req/s, burst 50, 10 auth failures per 15 min); token commands on the server *and* through the API and CLI (P5.3b).
- **[ADR-0011](adr/0011-api-limits-and-token-management.md)** covers both parts.
- **`internal/api/ratelimit.go`:**
  - Token buckets per client: one for requests, one for 401s.
  - It wraps the mux inside the request-ID and access-log middleware, so 429s are logged with an ID.
  - It skips `/healthz` and `/readyz`.
- **Client key:**
  - the last `X-Forwarded-For` entry (Caddy sets it and ignores the client's: `[CADDY-RP]`), IPv6 per /64;
  - otherwise the peer address, or `local` for a Unix-socket peer.
- **Memory:** bounded at 10,000 clients, with refilled ones swept first and an overflow bucket after that.
- **Config:** `SHIPYARD_API_RATE`, `SHIPYARD_API_BURST`, `SHIPYARD_AUTH_FAILURES` (0 turns a limit off), in `shipyard.env.example`.
- **Docs:** ARCHITECTURE §7, SOURCES (`RFC6585`, `RFC9110-RETRY`, `CADDY-IMAGE` modules), ROADMAP split, CHANGELOG.

**Decisions**
- **Count every 401** (bad or missing token, bad webhook signature), and refuse a throttled client before the token lookup, so a flood never reaches PostgreSQL.
- **Standard library only:** `golang.org/x/time/rate` would still leave the per-key map and its eviction to us.

**Verification**
- `make lint`, `make test` (race), `make test-integration`: exit 0. New unit tests:
  - `TestRateLimitRequests`: burst, then 429 with `Retry-After: 1` and problem+json; another client unaffected; `/healthz` exempt; a refill after 500 ms at 2/s;
  - `TestRateLimitAuthFailures`: after 3 bad tokens even a valid one gets 429 with `Retry-After: 60` and never reaches the token store; logged once; a minute later allowed;
  - `TestRateLimitMissingCredentialsCount`, `TestRateLimitOff`;
  - `TestClientKey`: 11 cases (last header and entry win, IPv6 /64, IPv4-mapped, zone, bad header, Unix peer);
  - `TestRateLimitBoundedClients`: 10,000 + overflow, swept when refilled;
  - `TestLoadAPIRateLimits`.
- `caddy list-modules` in the pinned Caddy image: 132 standard modules, none for rate limiting.
- `go test -tags e2e -run TestPhase1ExitCriteria ./test/e2e` with the default limits on: PASS, 509.4 s; no leftovers.
  - A first attempt was cut off when the WSL VM powered off mid-run ("The system will power off now!" in the journal; the host had slept).
  - With the owner's approval, I removed what it left: 3 containers, the e2e builder, and the e2e networks. I then reran it with keep-awake on.

**Next**
- P5.3b: `shipyard-api token rotate PREFIX [--grace D]`, `GET /v1/tokens`, `DELETE /v1/tokens/{prefix}`, `POST /v1/tokens/self/rotate`, and `shipyard token list|revoke|rotate`.

### 2026-10-04: P5.2 rootless BuildKit

- **Phase / task:** P5.2: rootless Docker and stronger build isolation: evaluation ADR
- **Author:** Claude Code (desktop session)

**Done**
- **Evaluation** (sources added or re-verified: `DK-ROOTLESS`, `DK-USERNS`, `DK-SECCOMP`, `DK-CONTAINERD`, `BX-PRIVILEGED`, `BK-ROOTLESS`, `UB-USERNS`, `GVISOR-DOCKER`, `SYSTEMD-EXEC`):
  - **Rootless Docker:** the host cannot reach container IPs (the health probes do), there is no AppArmor, and limits need cgroup delegation.
  - **userns-remap:** rules out the containerd image store.
  - **gVisor:** for untrusted apps, P7.
  - **Builds:** the remaining lever, with three options for the owner. **The owner chose rootless BuildKit** (2026-10-04).
- **[ADR-0010](adr/0010-rootless-buildkit.md).**
- **`build.Builder`:**
  - `DefaultImage` pins `moby/buildkit:v0.33.1-rootless` by index digest and goes in as `--driver-opt image=…`; until now buildx's floating default was used.
  - `Ensure` replaces a builder whose container (`buildx_buildkit_<name>0`) runs another image. That also moves pre-ADR-0009 builders onto the build network.
- **Host config:**
  - `deploy/sysctl/60-shipyard-buildkit.conf` (Ubuntu 24.04+).
  - `RestrictNamespaces=yes` in the api, worker and backup units.
  - deploy README, ARCHITECTURE, ROADMAP (P5.7 sub-item, risk row), CHANGELOG.

**Decisions**
- **Replace on image mismatch instead of keeping the old builder:** the builder is derived state, and keeping a root builder after the upgrade would leave the risk in place silently. The cost is one cold cache.
- **The sysctl is host-wide,** because buildx always makes the container privileged (so no AppArmor profile of our own applies to it). `RestrictNamespaces=` keeps our own services out of namespaces; containers already are, by Docker's seccomp profile without `CAP_SYS_ADMIN`.

**Verification**
- Exploration, WSL2 (kernel 6.18, no AppArmor; Engine 29.8.1, buildx 0.37.1), with throwaway builders that were removed afterwards:
  - rootful `RUN` uid map `0 0 4294967295`, buildkitd uid 0;
  - rootless `0 1000 1` + `1 100000 65536`, buildkitd uid 1000;
  - `adduser`, `chown`, `USER` work in both;
  - `Config.Image` equals the pinned reference as passed.
- `systemd-run --user -p RestrictNamespaces=yes unshare -Ur true`: "Operation not permitted". `git`, `docker`, `docker buildx` run under it.
- `make lint`, `make test`, `make test-integration`: exit 0. `go vet -tags e2e ./test/e2e/` and `-tags docker ./internal/build/`: ok.
- A container with Shipyard's flags (`--cap-drop ALL`, `no-new-privileges`): `unshare -U` fails with "Operation not permitted" `[DK-SECCOMP]`.
- `go test -race -tags docker -count=1 ./internal/build/`:
  - all pass except `TestBuilderRootless`, which failed on its own check: the uid map line starts with a space, so the log had two spaces after `uid_map`. The mapping itself was right (`0 1000 1`).
  - With the check fixed, `-run TestBuilderRootless`: PASS (98.6 s).
  - `TestBuilderReplacesOtherImage`: PASS (80.5 s).
  - No test builders or build networks left.
- `go test -tags e2e -run TestPhase1ExitCriteria ./test/e2e`: PASS, 485.4 s, on the rootless builder (failed-switch check: 791 probes, 43 answered by the candidate before the restore).
- `-run TestRestoreDrill`: PASS, 216.0 s.
- No leftovers after either e2e run.
- `TestCrashSafety`: not run (about 19 min, past this session's 10-minute run limit).

**Problems / surprises**
- The full `internal/build` Docker suite now takes about 590 s, close to this session's 10-minute limit for background runs. Run it in two halves next time.
- The WSL kernel has no AppArmor, so Ubuntu 24.04's userns restriction is taken from Ubuntu's and BuildKit's docs, not observed. The P5.7 installer must check it on a real host.
- A directory chowned in a rootless step kept the setgid bit (`drwxr-sr-x`).

**Next**
- P5.3: API rate limiting, auth-failure throttling, token revoke and rotate.

### 2026-10-04: P5.1 builder egress

- **Phase / task:** P5.1: builder egress control: evaluate a proxy or allow-list, then implement it or record an explicit deferral in an ADR
- **Author:** Claude Code (desktop session)

**Done**
- **Evaluation** (sources re-verified or added: `DK-BX-CONTAINER`, `DK-PREDEF-ARGS`, `DK-NET-INTERNAL`, `DK-26-DNS`, `BK-PROXY-NETWORK`). Three options went to the owner: an allow-list proxy on an internal network; BuildKit's `--proxy-network`; or deferral with cheap hardening. **The owner chose the deferral** (2026-10-04).
- **[ADR-0009](adr/0009-builder-egress-deferred.md):** the deferral, the residual risk, and when to revisit.
- **`build.Builder`:** `Ensure` creates the network `<builder>-build` (bridge, label `io.shipyard.role=build`) and puts the builder on it (`--driver-opt network=…`); `Remove` deletes it too. The e2e harness and the restore drill remove it with the builder.
- **P5.7 got a sub-item:** host firewall rules for that subnet (no cloud metadata, no host services).

**Decisions**
- **Not `--internal`:** an internal network would cut off the downloads builds need, and the deferral keeps egress open. A network of its own still takes the builder off the default bridge it shared with unrelated containers.
- **No migration for existing builders:** the network applies when a builder is created, like its limits. Shipyard has no production installs yet; the CHANGELOG says how to recreate one.
- **BuildKit's `--proxy-network` is only in `buildkitd --help` and issues;** `SOURCES.md` records it as checked, not as documented behaviour.

**Verification**
- `make lint`, `make test`, `make test-integration`: exit 0. `go vet -tags e2e ./test/e2e/` and `go vet -tags docker ./internal/build/`: ok.
- `go test -race -tags docker -count=1 ./internal/build/` (Engine 29.8.1, buildx 0.37.1): ok, 282.8 s; `TestBuilderNetwork` PASS (27.0 s). No test builders or `io.shipyard.role=build` networks left afterwards.
- `go test -tags e2e -run TestPhase1ExitCriteria ./test/e2e`: PASS, 482.5 s (failed-switch check: 648 probes, 42 answered by the candidate before the restore).
- `go test -tags e2e -run TestRestoreDrill ./test/e2e`: PASS, 199.5 s. No leftover containers or build networks after either run.
- `TestCrashSafety`: not run (about 19 min, longer than this session's run limit).

**Next**
- P5.2: rootless Docker and stronger build isolation (evaluation ADR).

### 2026-10-04: P4.6 deploy status in GitHub

- **Phase / task:** P4.6 (optional): report deployment status back to GitHub
- **Author:** Claude Code (desktop session)

**Done**
- **Owner's choices (2026-10-04):** the Deployments API, not commit statuses; every deploy and rollback of an app with an installation.
- **`github.Reporter`** (`CreateDeployment`, `CreateStatus`) and `App.TokenWith` (tokens with chosen permissions); **`app` reporting** hooks in the deployer (`report.go`); **migration `0004`**: `deployments.github_deployment_id`; `store.RecordGitHubDeployment`.
- Details: ARCHITECTURE §7 GitHub, "As implemented (P4.6)".

**Decisions**
- **`auto_merge: false`, `required_contexts: []`:** GitHub's defaults would merge the default branch into the ref, or refuse a commit with red checks; Shipyard reports the commit it deployed.
- **Shipyard marks the replaced release inactive,** because `auto_inactive` skips production environments `[GH-DEPLOY]`, and the apps are production.
- **No error text in GitHub:** a failure names its phase and the operation. GitHub shows statuses to everyone who can read the repository, so the text is limited to what that audience may see.
- **Best effort:** reporting never fails a deploy; a GitHub error is a warning event.
- **Not reported:** a deploy that fails before its commit is known, and an app's delete (its GitHub Deployments stay as they were).

**Verification** (WSL2, Engine 29.8.1, Caddy 2.11.4, PostgreSQL 18)
- `make lint`, `make test-integration` (migration `0004` applied in the store tests): ok. `make test` failed first in the new `TestReporterFails`: `../x` matched the repository pattern (dots are allowed characters) and would have reached the URL path. Fixed: `..` and a `.` segment are refused, as `internal/source` refuses `..`; then ok.
- New: `TestReporter` (the exact-commit body, one `deployments: write` token for all calls, a new one near expiry, the 140-character cut), `TestReporterFails`, `TestReportDeploy` (create, in progress, success with the URL, the replaced release inactive), `TestReportFailure` (phase and operation, no error text), `TestReportBestEffort`, `TestReportResumeAndRollback`.
- `TestPhase1ExitCriteria` (e2e): PASS (466 s). The fake GitHub now takes deployments and statuses with a `deployments: write` token only (that token does not open the git server). The private deploy reported `deployment <good> <app> false []`, `in_progress`, `success`; the two deploys that failed before their commit was known reported nothing.
- Not run: `TestRestoreDrill` and `TestCrashSafety` since P4.5; this task did not change the harness except the fake GitHub's new endpoints.

**Next**
- Phase 4 is done in code. Its exit criteria need a real GitHub App, webhook, and private repository on a VPS (the owner's).

### 2026-10-04: P4.5 catch-up

- **Phase / task:** P4.5: missed-delivery catch-up: compare the branch head with the last deployed SHA
- **Author:** Claude Code (desktop session)

**Done**
- **`source.Fetcher.Head`:** the branch tip by `git ls-remote`, exact ref only, with the same locked-down git environment and token header as a fetch.
- **`store.CatchUpCandidates`, `store.EnqueueCatchUp`;** **`app.CatchUp`** runs them per app; **worker:** a job at start and every `SHIPYARD_CATCHUP_INTERVAL` (5m), heads read through `sourceAdapter.Head` (App token when the app has an installation).
- Details: ARCHITECTURE §5, Reconciler step 4, "As implemented (P4.5)".

**Decisions**
- **"Last deployed SHA" made precise:** the head deploys only when the app never tried that commit (no deployment of it in any status, no earlier catch-up of it). Comparing with the active release alone would undo every rollback and retry a broken head on every pass.
- **Only apps deployed before.** Turning on auto-deploy for a new app does not deploy it by itself.
- **Its own job, not a reconciler step:** it calls GitHub once per app, so it runs every 5 minutes, not every minute, and a slow GitHub does not hold up container and route repair.
- **No audit event,** like the reconciler's rebuild: the operation's key (`catchup:<app>:<sha>`) records where it came from.

**Verification** (WSL2, Engine 29.8.1, Caddy 2.11.4, PostgreSQL 18)
- `make lint` (after one gofmt fix), `make test`, `make test-integration`: all ok. New: `TestHead` (source: exact ref only, even beside `refs/heads/x/refs/heads/main`; unknown branch; bad input; a private repository needs the token, sent in the header), `TestCatchUp` (store: candidates; no deploy of the deployed commit; one deploy of a new head, never twice, not retried after it failed; nothing after a branch change, with auto-deploy off, or while deleting), `TestCatchUp` (app: errors per app, the rest go on), `TestLoadWorkerCatchUpInterval`.
- `TestPhase1ExitCriteria` (e2e): PASS (413 s). New step: with the worker stopped, a commit is pushed to `e2e/demo` main and no webhook is sent; after the worker starts, the catch-up deploys it, the release is active, and Caddy serves its container.
- `TestRestoreDrill`: PASS (167 s), with the harness's new GitHub App settings and private repository.
- `TestCrashSafety`: not rerun since P3.7 (about 19 minutes, longer than one background run here); the harness changes since then are the worker's GitHub App settings and a second, private repository on the git server.

**Next**
- P4.6 is optional. Then the Phase 4 exit criteria on a real VPS with a real GitHub App and webhook (the owner's).

### 2026-10-04: P4.4 GitHub App

- **Phase / task:** P4.4: GitHub App: RS256 JWT, a per-operation installation token for one repository with `contents: read`, the key outside the database, private fetch
- **Author:** Claude Code (desktop session)

**Done**
- **`internal/github`:** `App.JWT`, `App.InstallationToken`, `LoadKey`. Standard library only (`crypto/rsa`, `encoding/pem`); no JWT dependency.
- **Worker:** `sourceAdapter` asks for a token before each fetch of an app with `github_installation_id` and passes it to `internal/source`, which already sent it as the `x-access-token` Basic header. The deploy's fetch event names the installation.
- **Config:** `SHIPYARD_GITHUB_APP_ID`, `SHIPYARD_GITHUB_APP_KEY_FILE` (both or neither), `SHIPYARD_GITHUB_API_URL`.
- **CLI:** `--github-installation ID` on `app create` and `app update`; `app show` prints it.
- Details: ARCHITECTURE §7 GitHub, "As implemented (P4.4)".

**Decisions**
- **The worker mints the token,** once per fetch, because only it talks to GitHub and git (invariant 1). No caching: a token lives for the one fetch, though GitHub's would last an hour.
- **The key is a file,** checked like the KEK files; RSA only (RS256), at least 2048 bits, PKCS#1 or PKCS#8.
- **Not done here** (P4.4 scope is fetching): matching pushes by repository id (the owner chose names for now) and learning the installation id from deliveries. Both are noted in ARCHITECTURE.
- **Mistake, caught before commit:** I wrote `cmd/shipyard-worker/adapters_test.go` with the file tool, not seeing it already existed, and replaced `TestContainerNamesAgree`. I restored it from git and added the new test beside it.

**Verification** (WSL2, Engine 29.8.1, Caddy 2.11.4, PostgreSQL 18)
- `make lint`, `make test`, `make test-integration`: all ok. New: `TestJWT` (claims and RS256 signature checked with the public key), `TestInstallationToken` (path, headers, the narrowed body; the token never prints), `TestInstallationTokenFails` (7), `TestLoadKey` (PKCS#1, PKCS#8; refuses world-readable, 1024-bit, EC, non-key PEM, garbage, missing, a directory), `TestSourceAdapterInstallation`, `TestLoadWorkerGitHubApp`, and `--github-installation` in `TestCLIEndToEnd`.
- `TestPhase1ExitCriteria` (e2e): PASS (374 s), now with `checkPrivateRepo`. The test runs a fake GitHub API (checks the JWT's signature, issuer, and lifetime; issues a token only for installation 77, narrowed to `private` with `contents: read`), and a git server that serves `e2e/private` only to that token as the `x-access-token` password. Results:
  - an app on `e2e/private` without an installation fails at fetch;
  - with installation 78 it fails with "GitHub refused an installation token";
  - with 77 it deploys and succeeds, its events name the installation and contain no token;
  - the app is then deleted.
- Not run: a real GitHub App on a real private repository (the Phase 4 exit criterion). It needs the owner's App and key.

**Next**
- P4.5: catch up on missed pushes in the reconciler (branch head vs. last deployed SHA), which needs the App to read branch heads of private repositories.

### 2026-10-04: P4.2, P4.3 deploy on push

- **Phase / task:** P4.2: delivery deduplication and idempotent enqueue; P4.3: repository-to-app mapping and branch policy. Done together: the enqueue needs the mapping.
- **Author:** Claude Code (desktop session)

**Done**
- **`store.RecordPush`:** one transaction records the delivery (`ON CONFLICT` on the delivery id), matches apps, enqueues a deploy per matching app, sets `outcome` and `operation_id`, and writes an audit event. Details in ARCHITECTURE §7, "As implemented (P4.2, P4.3)".
- **API:** `api.StorePushes` is the receiver's sink; the reply lists `operations`. A repeat delivery answers "received before" with the first operations.
- **`webhook.ParsePush`** now requires `repository.full_name` on branch pushes (it is what apps are matched by).
- **CLI:** `app update APP [--branch B] [--auto-deploy=true|false]` (`client.UpdateApp`), and `app create --auto-deploy`. ARCHITECTURE listed `app update`; the CLI never had it.

**Decisions**
- **Match by `owner/name`, case-insensitively** (the owner's choice, 2026-10-04, option 1 of 2). Correction to what I told the owner: an app's repo is fixed at create, so after a rename the app must be recreated, not updated, until P4.4 stores GitHub's repository id.
- **One deploy per matching app,** key `gh:<delivery>:<slug>`: nothing stops two apps from tracking the same repository and branch (a monorepo with two Dockerfiles). `operation_id` holds the deploy when there is exactly one.
- **Only pushes that reach the store are recorded.** Pings, other events, tags, and deleted refs are answered and logged, not stored.
- **No retention for `webhook_deliveries` yet.** GitHub redelivers for 3 days `[GH-REDELIVER]`, so rows must outlive that; a cap in the retention job is a follow-up.

**Verification** (WSL2, Engine 29.8.1, Caddy 2.11.4, PostgreSQL 18)
- `make lint`, `make test`: ok. `make test-integration`: ok except `TestLeaseHandover` (the known flake) and `TestEnqueueRebuild` (an empty claim at line 491, code this task did not touch); a rerun of `store` and `queue` passed both. New: `TestRecordPush`, `TestRecordPushPolicy`, `TestRecordPushConcurrent` (8 at once: one record, one deploy), `TestRecordPushRollsBack`, two `TestParsePush` cases, and `app update` in `TestCLIEndToEnd`.
- `TestPhase1ExitCriteria` (e2e): PASS (442 s). Through Caddy: a push for a repository no app deploys is ignored; a push of `E2E/Demo` main with auto-deploy off is ignored; after `app update --auto-deploy` the same push deploys `good`, the operation succeeds, and Caddy serves the new container; redelivering that delivery answers "received before" with the same operation, and the release history is unchanged.
- The first combined run hit the 10-minute background limit during the e2e; its containers (`shipyard-e2e-4x4ejsdo-*`, `shipyard-e2e-m3c36is2-caddy`, `buildx_buildkit_shipyard-e2e-m3c36is20`) were left on the owner's machine, not removed.

**Next**
- P4.4: the GitHub App. Its JWT, installation tokens, and key storage are vendor facts to re-verify first (`GH-APP-JWT`, `GH-APP-TOKEN`).

### 2026-10-04: P4.1 webhook receiver

- **Phase / task:** P4.1: `internal/webhook`, verify before parsing, answer within 10 s, only `push`
- **Author:** Claude Code (desktop session)

**Done**
- **`internal/webhook`:** `Verify` (`sha256=` + hex, `hmac.Equal`, the SHA-1 header never read), `Sign`, `ParsePush` (ignores deleted refs and non-branch refs; rejects a missing repository id, a bad ref, or an `after` that is not a full SHA), `ValidDelivery`, and `LoadSecret`.
- **`POST /hooks/github`** in the API, outside token auth; order of checks and answers in ARCHITECTURE §7 GitHub. A verified push to a branch goes to a `PushSink` (3 s deadline, 503 on failure). There is no sink yet, so pushes are answered `ignored`.
- **Config:** `SHIPYARD_GITHUB_WEBHOOK_SECRET_FILE`; unset turns the endpoint off (404).

**Decisions**
- **The secret is a file**, checked like the KEK files (regular, not readable by other users), at least 16 bytes. One secret serves every webhook, which fits both a GitHub App's single webhook (P4.4) and repository webhooks.
- **Nothing is written before verification.** Failures before it are logged without payload or unvalidated headers, as for anonymous API failures. Verified deliveries are recorded in P4.2 (`webhook_deliveries`, actor `webhook`), not in the audit table.
- **415 for form-encoded deliveries**, after the signature check, so an unsigned request always gets the same 401.
- **No ADR:** the behaviour was already specified in ARCHITECTURE §7; this records how.

**Verification** (WSL2, Engine 29.8.1, Caddy 2.11.4, PostgreSQL 18)
- `make lint` (after one gofmt fix), `make test`, `make test-integration`: all ok. New: `TestSignMatchesGitHub` (GitHub's published test vector), `TestVerifyRejects` (15 cases), `TestParsePush` (15), `TestValidDelivery`, `TestLoadSecret`, `TestWebhookPush`, `TestWebhookIgnored` (6), `TestWebhookRejects` (13: each answer is problem+json, the sink and the audit table untouched, no payload or secret in the log), `TestWebhookNotConfigured`, `TestLoadAPIWebhookSecretFile`.
- `TestPhase1ExitCriteria` (e2e): PASS (300 s). The real API, with the secret from its file, behind Caddy over HTTPS: a signed `ping` → 200 `pong`, a signed push → 202 `ignored`, a push signed with another secret → 401.
- The rest of the e2e suite was not rerun: nothing it covers changed besides the harness's API environment.

**Next**
- P4.2: deliveries into `webhook_deliveries` (dedup on the delivery id) and the idempotent enqueue under `gh:<delivery-id>`; it needs P4.3's repository-to-app mapping to know which app a push deploys.

### 2026-10-01: P3.8 delete app

- **Phase / task:** P3.8: delete app as an operation. The last task of Phase 3.
- **Author:** Claude Code (desktop session)

**Done**
- **Migration `0003`:** `operations.kind` also allows `delete`.
- **`app.Deleter`** (worker, kind `delete`), phases `release` → `containers` → `network` → `images` → `remove`; see ARCHITECTURE §6, "Deleting an app".
- **Store:** `StopServing` (routes deleted, the active deployment superseded, in-progress ones cancelled), `AppArtifacts`, `FinishAppDelete` (audit event and the app row in one transaction); each needs the delete operation's lease. `EnqueueOperation` returns a pending delete for another delete and `ErrAppDeleting` for anything else.
- **API:** `DELETE /v1/apps/{app}` queues the operation (202, or 200 for a pending one). A delete operation's event stream ends as `succeeded` when the operation has vanished with its app. `ErrAppDeleting` is a 409.
- **CLI:** `shipyard app delete APP --yes [--follow]`; `client.DeleteApp`, `client.IsNotFound`.
- **Removed:** `store.DeleteIdleApp` and `ErrAppBusy`: the API no longer deletes rows itself.

**Decisions**
- **The rows go at the end, by the existing cascade.** The schema already cascades an app's rows and keeps audit events forever, so the delete operation removes itself with the app, and the audit event is the lasting record. A soft delete would have changed the data model; not needed for this task.
- **`release` comes first:** with the routes gone and no deployment active in the database, the reconciler does not restore the containers the next step removes, and Caddy never points at a container that is gone.
- **Only what this database recorded is removed,** as everywhere since P2.6. This closes the open risk "deleting an app leaves its containers running".
- **A failed delete leaves the app out of service** and partly removed; repeating the delete finishes it. Going back into service after `release` would need a deploy.
- **`--yes` is required** in the CLI: the command is not undoable.
- **API behaviour change:** before, `DELETE` removed an idle app at once (204) and refused a serving one (409, "stop it first", with no way to stop it). That behaviour was never released (v0.1.0 is Phase 0).

**Verification** (WSL2, Engine 29.8.1, Caddy 2.11.4, PostgreSQL 18)
- `make lint` (after two gofmt fixes), `make test`, `make test-integration`: all ok. New: `TestDelete`, `TestDeleteFails` (7 cases), `TestDeleteLeaseLostAndResume`, `TestAppDelete` (store), `TestAppDeleteEndpoint` (API), and the delete sections of `TestAppsCRUD` and `TestCLIEndToEnd`.
- `make test-e2e`, full run: `TestCrashSafety` PASS (1142 s), `TestRestoreDrill` PASS (288 s), `TestPhase1ExitCriteria` FAIL in the P3.2 restore step, before the delete: the P3.4a flake again, now with the diagnostics. Docker answered `docker stop` with "is not running", while the container had been running at the stop (a stop of an already stopped container is a 304, no error), stopped about 1 s later, and was started again by the reconciler. No worker path stopped it (no log line). The test now accepts that answer only when the container finished after the stop was issued.
- Rerun of `TestPhase1ExitCriteria`: PASS (311 s), ending with `shipyard app delete <slug> --yes --follow`: the host no longer served by Caddy, the app's containers, network, and labelled images gone, the app no longer listed, Caddy still running. (The operation's 404 and the `app.delete` audit event are checked in `TestAppDeleteEndpoint`.) No leftover containers.

**Next**
- Phase 3 exit criteria (ROADMAP): the owner's part is the restore drill on a real VPS. Then Phase 4 (GitHub App and webhooks).

### 2026-10-01: P3.7 crash-safety suite

- **Phase / task:** P3.7: kill the worker at every phase boundary, then assert a consistent state
- **Author:** Claude Code (desktop session)

**Done**
- **Fault points** (`app.FaultPoints`, 11): the six phases, each at the moment it is persisted (`fetch`, `build`, `start`, `health`, `switch`, `activate`), and five gaps between a side effect and its record (`built`, `created`, `started`, `switched`, `committed`). `Deployer.Fault` is called at each; it is nil in production.
- **`SHIPYARD_TEST_CRASH_AT`:** the worker kills itself (`os.Process.Kill`, SIGKILL on Linux) on reaching the point. An unknown point is a startup error.
- **`SHIPYARD_WORKER_LEASE`** (default 1m, 2s–1h): the lease was fixed at `queue.DefaultLease`. The suite uses 3s.
- **`TestCrashSafety`** (e2e): for each point, a doomed worker, a deploy, the state while it is dead, then a fresh worker.
- e2e harness: `spawn` returns a `proc` with `stop` and `exited`. `make test-e2e` has a 45m timeout (three tests).

**Decisions**
- **The kill is inside the worker,** at a named point, so each crash lands exactly on the boundary. An outside `kill -9` could not be timed that precisely.
- **Gaps as well as phase boundaries:** the windows that matter most are between a side effect and its record (a container that exists but is not recorded).
- **No fix was needed:** every point recovered on the first run. The suite is now the regression guard for invariant 3.

**Verification** (WSL2, Engine 29.8.1, Caddy 2.11.4, PostgreSQL 18)
- `make lint`, `make test`, `make test-integration`: all ok.
  - Unit: `TestFaultPoints` (a deploy with routes passes all 11 points once, in order; a rollback skips the fetch and build points; without routes nothing is switched), `TestCrashAt` (an unknown point is refused; the hook returns at every other point), `TestLoadWorkerLeaseAndCrashPoint` (defaults, set values, 8 bad values).
- **`TestCrashSafety`: PASS, twice** (625 s alone; 479 s in the full run). At each of the 11 points:
  - the doomed worker exits; the operation is `running` on attempt 1 in the expected phase (`succeeded` at `committed`), and its deployment has the expected status (none at `fetch`);
  - exactly one deployment is active: the previous one, or the new one at `committed`;
  - Caddy answers from the previous container, or from the candidate at `switched`, `activate`, and `committed`;
  - after a fresh worker starts, the operation succeeds on attempt 2 (1 at `committed`) with the same deployment, the previous one is superseded, the history grew by exactly one, one container is left, and Caddy serves it.
- **`make test-e2e`: PASS (943 s)**, all three tests: `TestCrashSafety`, `TestPhase1ExitCriteria` (295 s), `TestRestoreDrill` (169 s). No leftover containers or builders.

**Problems / surprises**
- Between the Caddy switch and the commit (`switched`, `activate`), a dead worker leaves Caddy on the verified candidate while the routes table names the previous release. ARCHITECTURE's failure table said "unchanged, or equal to the DB"; it now states this window. The next worker start loads the table.

**Next**
- P3.8: delete app as an operation (containers, network, route, images, audit event).
- Not covered by the suite: a crash during a rollback, in the reconciler, or of the API.

### 2026-10-01: P3.6b restore and drill

- **Phase / task:** P3.6b: `shipyard-worker restore`, the runbook, and the automated drill. P3.6 is done on the Shipyard side.
- **Author:** Claude Code (desktop session)

**Done**
- **`shipyard-worker restore --from DIR`** (`backup.Restore`):
  - checks first, changing nothing on a failure: the manifest (version, only the two known file names, sizes, SHA-256), every KEK it names in `SHIPYARD_KEK_DIR`, and an empty database (`store.Empty`);
  - then Caddy's data (`runtime.RestoreEdgeData`: ensure the edge, stop it, copy the re-rooted archive into `/data`, start it);
  - then `pg_restore --single-transaction --no-owner --no-privileges`, the dump on standard input, the password in `PGPASSWORD`. `SHIPYARD_BACKUP_PG_RESTORE` names the tool.
- **`docs/RESTORE.md`:** the runbook (7 steps), what comes back and what does not, and what to do when a step fails.
- **`TestRestoreDrill`** (e2e): deploy, back up, lose the host, restore, start; the app returns by itself.
- e2e harness: `spawn` returns a stop function; `backup()` and `pgTool()` are shared with the P3.5 check.

**Decisions**
- **Caddy first, the database last:** the database load is the only step that cannot be repeated over itself, and it is atomic. A run that fails anywhere can be repeated unchanged.
- **The operator puts the KEKs back, not the restore:** only root writes the KEK directory, and the keys travel apart from the data (ADR-0005). The restore refuses to start without them.
- **The archive is rewritten on the way in** (`rerootArchive`): Docker refuses to extract at `/` of a read-only container but accepts the volume path, and Caddy (root without `CAP_DAC_OVERRIDE`) must own the files `[DK-CP]`.
- **The restore creates the edge without the API socket mount:** that directory exists only once the API has run. The worker recreates the edge with it at its start, keeping the volumes.
- **Marked done with a caveat:** the drill runs on one machine (a "container host", as the roadmap item allows). The first run on a real VPS, which ADR-0006 requires before "production-ready", is the owner's. The host commands in the runbook are not yet run on a real server.

**Verification** (WSL2, Engine 29.8.1, Caddy 2.11.4, PostgreSQL 18)
- `make lint` (after a gofmt of `restore_test.go`), `make test`, `make test-integration`: all ok.
  - `internal/backup`: `TestRestore` (Caddy, then pg_restore; the dump on standard input; the exact arguments; the password only in `PGPASSWORD`; Caddy disabled skips its data), `TestRestoreRefused` (11 cases, nothing touched: a damaged dump, truncated Caddy data, a missing file, no manifest, a path as a file name, no dump listed, a newer manifest, a path as a KEK ID, a missing KEK, a database with tables, an unreachable database), `TestRestoreFails` (pg_restore's output reported; a Caddy failure stops before the database).
  - `internal/runtime`: `TestRerootArchive` (prefix removed, root-owned, modes and contents kept), `TestRerootArchiveRefuses` (5 names outside `data/`, and garbage).
  - `cmd/shipyard-worker`: `TestRun` (`restore` without, with an empty, or with extra `--from` arguments exits 2).
  - Integration: `TestEmpty` (a new database is empty; a migrated one is not).
- Docker: `TestArchiveEdgeData` now also restores the archive into a fresh edge, twice: the file is there with mode 600, owned by 0:0, and the admin API answers; garbage is refused. `TestEdgeBootstrap`: ok.
- **`make test-e2e`: PASS (591 s), both tests.** `TestRestoreDrill` (234 s; 215 s when rerun alone with the `shipyard-api migrate` step):
  - the restore is refused without the KEK, and into the old database, and creates no Caddy container;
  - after the restore, `migrate`, and starting both services, a new container runs the active commit;
  - the history is "active, superseded" on that commit, read with the API token from the backup;
  - `GREETING` is the value the release ran with, not the one set after the deploy;
  - Caddy serves a certificate with the same SHA-256 as before the host was lost.
- No leftover containers or builders.
- Not run: the runbook's host commands (`install`, `createdb`, `systemd-run`) on a real server.

**Problems / surprises**
- `docker cp` into `/` of the Caddy container fails: "container rootfs is marked read-only". Into `/data` it works, and the files keep the archive's owner IDs. Found by a probe before writing the code.

**Next**
- P3.7: the crash-safety suite (`kill -9` the worker at every phase boundary through fault-injection hooks).
- Owner: the restore drill on a real VPS; PRs for `retention-caps`, `backup`, `rebuild`, and `restore`.

### 2026-10-01: P3.6a rebuild

- **Phase / task:** P3.6a: the reconciler rebuilds an active deployment whose image is gone (P3.6 split: P3.6b is the restore command, the runbook, and the drill)
- **Author:** Claude Code (desktop session)

**Done**
- **Reconciler:** an active deployment whose container is gone is recreated from its image as before. If the image is gone too, it calls `store.EnqueueRebuild` once per deployment (`Runtime.ImageExists` first). A rebuild queued or running is left alone; a failed one is reported on every pass; a busy app is left to its operation.
- **`store.EnqueueRebuild`:** a `deploy` operation with key `rebuild:<deployment>`, under the app lock. It cancels nothing. `ErrConflict` when the deployment is no longer active or the app has an operation queued or running.
- **Deploy:** `DeployPayload.RebuildOf`. `prepareRebuild` requires the deployment to be this app's and still active, then the deploy fetches its commit and pins its environment revision instead of the latest.

**Decisions**
- **Why the reconciler rebuilds at all:** ADR-0006 says apps are rebuilt from their recorded SHAs after a restore, and P3.6 wants the reconciler to converge every app. This replaces P3.2's "log until a deploy or rollback" for a missing image.
- **The deployment's revision, not the latest:** a restore brings back what was running. A later `env set` without a deploy is not applied by a restore.
- **One attempt:** a rebuild that failed (repository unreachable, commit no longer on the branch) is not retried in a loop. The operator deploys or rolls back.
- **Never backwards:** no rebuild for a busy app, and a rebuild that runs after its deployment was replaced fails without building.

**Verification** (WSL2, PostgreSQL 18)
- `make lint`, `make test`, `make test-integration`: all ok.
  - Unit: `TestRebuild` (the target's commit and revision, not the latest; a target without an environment), `TestRebuildRefused` (superseded, another app's, unknown: no fetch, build, or container), `TestPassRebuilds` (requested, queued, running, failed, busy app, database error).
  - Integration: `TestEnqueueRebuild` (created once; the same operation after it failed; superseded and unknown deployments refused; a busy app refused and its queued operation kept), `TestDeployEndpoint/rebuild_of` (the API refuses the field, 400).
- Not run: Docker or e2e. Nothing Docker-facing changed; the restore drill of P3.6b proves the rebuild end to end.

**Next**
- P3.6b: `shipyard-worker restore`, `docs/RESTORE.md`, and the drill.

### 2026-10-01: P3.5 backup

- **Phase / task:** P3.5: backup (ADR-0006)
- **Author:** Claude Code (desktop session)

**Done**
- **`shipyard-worker backup`** (`internal/backup.Job`), one run and exit:
  - **Target A** (`SHIPYARD_BACKUP_DIR`): `<UTC time>/database.dump` (`pg_dump --format=custom`, checked for the `PGDMP` magic), `caddy-data.tar.gz`, and `manifest.json` (sizes, SHA-256, KEK IDs). It is written under `.partial-…` and renamed when whole.
  - **Target B** (`SHIPYARD_BACKUP_KEK_DIR`): the KEK files, add-only. A KEK changed under the same ID is an error.
  - **Hooks** (`SHIPYARD_BACKUP_HOOK`, `SHIPYARD_BACKUP_KEK_HOOK`): `sh -c` with `SHIPYARD_BACKUP_PATH`, `PATH`, and `HOME` only.
  - **Rotation** (`backup.Expired`, pure): the newest backup of each of the last 14 days and 8 ISO weeks; after each successful backup, even if the hook fails.
- **`runtime.ArchiveEdgeData`:** the Caddy container's `/data` through Docker's archive API; managed edge containers only.
- **Config:** the 7 `SHIPYARD_BACKUP_*` settings. A, B, and the live KEK directory must not contain one another.
- **Deploy:** `shipyard-backup.service` (oneshot, the worker's user, `UMask=0077`, `TimeoutStartSec=2h`, `ReadWritePaths=` for both targets) and `shipyard-backup.timer` (02:30, `Persistent=true`).

**Decisions** (the first two with the owner, recorded as a note in ADR-0006)
- **Targets are local directories with a hook each,** so Shipyard depends on no upload tool.
- **A worker subcommand, not shell scripts.**
- **The password goes in `PGPASSWORD`,** not on pg_dump's command line, which `ps` shows. libpq warns about `PGPASSWORD` where other users can read a process's environment; on Linux that needs ptrace access `[PG-DUMP]`.
- **All of A or nothing:** a restore never meets a half backup. The KEK backup is independent and runs even when the data backup fails.
- **Not in the backup:** `shipyard.env` (ADR-0006 wants its copy encrypted, which needs a tool and key this task does not add). Left for P3.6's runbook; flagged to the owner.
- **Size:** about 1,270 lines with tests and docs (about 600 of code outside tests), well over the ~400 guide. The parts are not useful apart.

**Verification** (WSL2, Engine 29.8.1, PostgreSQL 18, systemd 255 man pages)
- `make lint` (after gofmt of `config.go` and the e2e test), `make test`, `make test-integration`: all ok.
  - `internal/backup`: `TestRun` (the three files, owner-only modes, the manifest's checksum and KEK IDs, both hooks, no password in pg_dump's arguments, the hook's environment, or the log), `TestRunDataFails` (a failed dump, wrong output, empty output, a failed Caddy archive: nothing left in A, the older backup kept, the KEKs still copied, no hook), `TestBackupKeys` (add-only; a changed key refused and not overwritten; no keys is an error), `TestRotateAndHookFailure` (expired backups and a stale partial directory removed, other entries untouched, a hook's failure reported, the same second refused), `TestDumpCommand`, `TestExpired` (8 cases, including a same-day pair and an ISO week across the new year).
  - `TestLoadWorkerBackup`: defaults, set values, 12 refused settings.
- Docker: `TestArchiveEdgeData` (a 0600 file in `/data` arrives with its mode, from a running and a stopped edge; no edge is `ErrNotFound`) and `TestEdgeRefusesForeignContainer` (a foreign container is not archived): ok.
- **`make test-e2e`: PASS (363 s).** After the deploys and the rollback, `shipyard-worker backup` beside the running worker wrote one backup: `database.dump` starts with `PGDMP`, and `pg_restore --list` (PostgreSQL 18) shows the data of `deployments`, `secret_values`, `routes`, and `operation_events`; `caddy-data.tar.gz` holds Caddy's local CA; the manifest names the KEK; target B has the same key bytes; the hook listed the new backup. No leftovers.
- Not run: the systemd units (no install on this machine yet; P5.7). Their options are checked against the man pages (`SYSTEMD-TIMER`).

**Problems / surprises**
- Docker's docs do not say whether `docker cp` includes a mounted volume. A probe showed it does, with file modes, for a running and a stopped container (`DK-CP`).
- The test host has no `pg_dump`. The e2e uses the `postgres:18` image through a wrapper, which is also why the job takes `SHIPYARD_BACKUP_PG_DUMP`.

**Next**
- P3.6: the restore runbook and an automated drill: `pg_restore` the dump, put back the KEKs and Caddy's data, and let the reconciler converge.

### 2026-09-30: P3.4b cache and log caps

- **Phase / task:** P3.4b: the BuildKit cache cap and the operation event cap (ADR-0006). P3.4 is done.
- **Author:** Claude Code (desktop session)

**Done**
- **`app.Retention`**, run by the worker at start and every `SHIPYARD_RETENTION_INTERVAL` (default 24h) through `reconcile.Every` (the reconciler's loop, extracted). Both steps run even if one fails.
- **`build.PruneCache`:** `buildx prune --builder <name> --force --max-used-space <bytes>`; returns the reclaimed size from the `Total:` line. It takes the builder's one slot (shared with `Build` via `acquire`/`release`), so it never runs beside a build.
- **`store.TrimOperationEvents(keep, maxBytes)`**, finished operations only:
  - beyond each app's newest `keep`, all events go (rows stay);
  - over `maxBytes`, the middle goes, except warnings and errors, and one warning marker takes the first removed seq.
- **Config:** `SHIPYARD_RETENTION_INTERVAL` (≥ 1s), `SHIPYARD_BUILD_CACHE_MAX` (default 10g, ≥ 1 MiB), `SHIPYARD_RETAIN_OPERATIONS` (default 20, 1–10000), `SHIPYARD_OPERATION_LOG_MAX` (default 5m, ≥ 1 KiB). Sizes are binary (`k`/`m`/`g`/`t`, optional `b`).

**Decisions**
- **Trim the middle, not the end:** the start (what was built) and the end (why it failed or what went live) are what an operator reads; warnings and errors always stay. A marker says what was removed.
- **Finished operations only:** a running operation's stream never has events disappear under it.
- **Prune serialized with builds:** the docs do not say whether a prune leaves alone the cache a build is using, so the prune does not rely on it.
- **A plain byte count to buildx:** our units are binary, and buildx's suffix parsing is not ours to depend on.

**Verification** (WSL2, Engine 29.8.1, buildx 0.37.1, PostgreSQL 18)
- `make lint` (after a gofmt of `config.go`), `make test`, `make test-integration`: all ok.
  - Unit: `TestRetentionRun` (the order prune → trim, the configured limits, a failing step not skipping the other), `TestLoadWorkerRetention` (defaults, set values, 14 bad values), `TestParseSize`, `TestRun` (via `Every`).
  - Integration: `TestTrimOperationEvents`: with keep 2, an old finished operation loses its events while an older queued one, the running one, and the newest keep theirs. An 1100-byte log capped at 400 keeps seq 1–2, the marker at 3 ("6 log lines (600 bytes)"), the error at 6, and 10–11. Resume after the marker works, and a second pass is a no-op.
- Docker: `TestPruneCache` (a 2 MiB build: nothing reclaimed under a 1 TiB cap, something at 1 byte, `0B` the second time; a zero cap refused), `TestBuildSucceeds`: ok.
- **`make test-e2e`: PASS (300 s)** with `SHIPYARD_RETENTION_INTERVAL=3s` and `SHIPYARD_RETAIN_OPERATIONS=2`. The first deploy's events are removed while its operation still reads succeeded, and the newest operation keeps its events. The P3.4a image checks pass too. No leftover containers or builders.

**Next**
- P3.5: backup (`pg_dump -Fc` and a Caddy data tarball to target A, the KEK to target B, by systemd timers).

### 2026-09-30: P3.4a image retention

- **Phase / task:** P3.4a: image retention per ADR-0006 (P3.4 split: P3.4b is the build cache and event caps)
- **Author:** Claude Code (desktop session)

**Done**
- **`store.PrunableImages(present, keep)`:** of the images present on the host, those a deployment here recorded and retention does not keep. Kept per app: the active image, the last `keep` superseded images ranked by when each last served (an image served twice counts once), images of deployments in progress, and the target of a queued or running rollback.
- **`app.ImagePruner`:** lists managed images, asks the store, removes. An image in use stays for a later pass; other failures are joined.
- **`runtime.ListImages`** (by the `io.shipyard.managed` label, so untagged images are included) and **`runtime.RemoveImage`** (managed images only, never forced; `ErrInUse` on a 409).
- **Reconciler step 5**, after the janitor. **Config:** `SHIPYARD_RETAIN_IMAGES` (default 5, 0–1000).
- **e2e cleanup** now removes the app's images by label. The name filter missed untagged ones: 43 images from earlier e2e runs were left on the owner's machine (not removed; see Next).

**Decisions**
- **Rank by distinct image, not by deployment row:** rolling back and forth between two releases would otherwise use up the rollback depth.
- **Run every reconcile pass,** not daily: one list and one query, and disk frees soon after a failed deploy. The BuildKit prune (P3.4b) stays daily per ADR-0006.
- **An image the database never recorded is never removed,** as with containers (P2.6). This also covers the window between a build and `RecordImage`.

**Verification** (WSL2, Engine 29.8.1 with the containerd image store, PostgreSQL 18)
- `make lint` (after two gofmt fixes), `make test`, `make test-integration`: all ok (`TestLeaseHandover` failed once in an earlier run: the known pre-existing flake).
  - Unit: `TestImagePrune` (the in-use image kept quietly, another failure reported without stopping the pass), `TestImagePruneNothing`, `TestPass` (step 5 after the sweep), `TestLoadWorkerRetainImages`.
  - Integration: `TestPrunableImages`: keep 2/0/5, per-app ranking, a twice-served image counted once, the active and in-progress images kept, only present and recorded images returned, and a queued rollback's target kept.
- Docker: `TestRemoveImage` (a tagged and an untagged image removed; a stopped container's image and a twice-tagged one kept as `ErrInUse`; a foreign image `ErrNotManaged`; a tag `ErrInvalid`; a missing image ok), `TestImageExists`, `TestListManaged`: ok.
- `make test-e2e` with `SHIPYARD_RETAIN_IMAGES=1`, first run: the retention checks passed (the two failed deploys' images removed, the active and previous ones kept). The run then failed later in the P3.2 restore step: the recreated container had stopped by the time the test ran `docker stop`, just after a `docker rm --force` the reconciler had raced ("marked for removal"). No retention action ran then. The test now logs the container's state if this happens again.
- **`make test-e2e` rerun: PASS (298 s).** The images of the failed deploys (steps 4 and 4b) were removed; the active and the superseded first release's images stayed, and after the rollback the pair is the first and second release's. No leftovers.

**Problems / surprises**
- On the containerd image store, `docker build` on the `docker` driver deletes the image whose tag it takes over; `docker tag` and `buildx build --load` leave it untagged. My first Docker test relied on the former and failed. Recorded as `DK-RMI`.

**Next**
- P3.4b: `buildx prune --max-used-space` daily (flag verified on buildx 0.37.1; `--keep-storage` no longer exists) and the operation event cap.
- Owner: remove the 43 leftover untagged images of `e2e-*` apps if wanted (only those whose `io.shipyard.app` label starts with `e2e-`).

### 2026-09-30: P3.3 rollback

- **Phase / task:** P3.3: the rollback operation
- **Author:** Claude Code (desktop session)

**Done**
- **API** `POST /v1/apps/{app}/rollbacks` (`deploy` scope):
  - `to` must be a superseded deployment of the app: an active one is 409, one that never served is 422, and another app's or an unknown one is 422;
  - the same idempotency and latest-wins rules as a deploy apply (a shared `admit`);
  - when secrets changed, the request is 409 naming the keys, unless `with_old_config` or `with_current_config` is given (both at once is 422).
- **`app.RotatedSecrets`:** the target's secret keys whose value row differs in the latest revision, or that were removed or made plain since. Keys only; nothing is decrypted.
- **Worker:**
  - `Deployer` runs `kind = rollback`: `prepareRollback` checks the target, then `ImageExists`. A missing image fails the operation at once with "rollback unavailable" and a hint to deploy the commit again.
  - `store.CreateRollbackDeployment` records the target's commit, image, and build metadata, `source_deployment_id`, and the chosen revision, in `starting`. Start, health, switch, and activate are unchanged, and a retry resumes.
  - `runtime.ImageExists`. The worker no longer fails unknown kinds as "not supported yet".
- **History:** `Deployment.SourceDeployment` is read everywhere, and the releases JSON has `rollback_of`, shown by the CLI as "rollback of …".
- **CLI:** `shipyard rollback APP --to ID|prefix [--with-current-config|--with-old-config] [--idempotency-key] [--follow]`. It resolves a prefix among the latest 100 deployments.

**Decisions**
- **Where "unavailable" is decided:** only the worker can see images (invariant 1). So an unavailable rollback is the operation's immediate failure, not an API answer. This is noted on the roadmap item.
- **"Warns and offers":** a 409 that names the rotated keys and requires an explicit choice, rather than a silent default. Rolling back to old credentials may be intended, or dangerous.
- **Removed or made-plain secrets** count as rotated: the old revision would bring the value back.
- **Size:** about 700 lines with tests, more than the ~400 guide. The API and worker halves are not useful apart, so it stayed one slice.

**Verification** (WSL2, Engine 29.8.1, Caddy 2.11.4, PostgreSQL 18)
- `make lint` (after a gofmt of `rollback_test.go`), `make test`, `make test-integration`: all ok.
  - Unit: `TestRollback` (no fetch or build; the target's image, commit, and revision; kind and source), `TestRollbackWithCurrentConfig` (the latest revision, and none at all), `TestRollbackRefused` (5 cases, no side effects), `TestRollbackResumes`, `TestRotatedSecrets`.
  - Integration: `TestCreateRollbackDeployment` (the copy, the link, `starting`; a cross-app or unbuilt source refused) and `TestRollbackEndpoint` (202, replay, the rotated 409 without values, both flags, 8 negative cases).
- Docker: `TestImageExists` (present and absent) ok.
- **`make test-e2e`: PASS (269 s).**
  - With `GREETING` changed, `rollback --to <prefix>` is refused and names `GREETING`.
  - `--with-old-config --follow` succeeds. The previous container drains, Caddy serves the rollback's container with the old `GREETING`, and `releases` shows it active as "rollback of <prefix>".
- No leftovers.

**Next**
- P3.4: retention (ADR-0006). Images of the last 5 successful deployments per app are what keeps rollback available.

### 2026-09-30: P3.2 reconcile package

- **Phase / task:** P3.2: `internal/reconcile`, run at startup and every 60 s
- **Author:** Claude Code (desktop session)

**Done**
- **`internal/reconcile.Reconciler`** replaces the worker's inline loop. One pass:
  1. requeue or fail expired operations;
  2. **restore active containers** (new);
  3. sync Caddy;
  4. run the janitor.
  Each step carries on past a failing item, and `Run` passes at start and then every interval.
- **Restore:**
  - a running or restarting container is left alone, and a stopped one is started;
  - a missing one is recreated from the deployment's image ID, environment revision, and app limits, under the same name, recorded (`store.ReplaceContainer`, only while still active with the old ID), then started;
  - a pruned image or a deployment superseded meanwhile is logged and skipped. The janitor removes a container recreated for a now-superseded deployment.
- **Ports:** `app.ErrContainerGone`; the worker's `Inspect` adapter maps `runtime.ErrNotFound` to it. `store.ActiveDeployments`.

**Decisions**
- **No health gate on restore:** it is the same release, from the same image and configuration, that already passed one (invariants 5 and 6). The restart policy handles crash loops.
- **The same deterministic name** means the routes resolve to the new container at once. No Caddy change is needed.
- Orphans without a database row are still left alone (P2.6), and deleted apps wait for P3.8. This is noted on the roadmap item.

**Verification** (WSL2, Engine 29.8.1, Caddy 2.11.4, PostgreSQL 18)
- `make lint` (after a gofmt of the test), `make test`, `make test-integration`: all ok. `-race -count=5` on `reconcile`: ok.
  - `TestPass`: the order requeue → start the stopped container → create → record → start → sync → sweep, and running and restarting containers left alone.
  - `TestPassCarriesOn`: the image is gone, and the deployment was superseded meanwhile (not started).
  - `TestRun`: synctest, 4 passes in 3 minutes.
  - `TestReplaceContainer`: all active deployments listed; a stale or superseded deployment gives `ErrConflict`.
- **`make test-e2e`: PASS (271 s).** `docker rm -f` of the active container: within about a second a new one runs under the same name, and Caddy serves it with `GREETING` intact. `docker stop`: the same container is started again, and Caddy serves it.
- No leftovers. Docker tests were not run (no runtime or routing change).

**Next**
- P3.3: rollback.

### 2026-09-29: P3.1 release history

- **Phase / task:** P3.1: deployment history, `GET /v1/apps/{id}/deployments` and `shipyard releases APP`
- **Author:** Claude Code (desktop session)

**Done**
- **`store.Releases`:** newest first, ordered by `(created_at, id)`, with a keyset cursor (`before`). A cursor that is not this app's deployment is `ErrNotFound`. Each row carries its environment revision number from a scalar subquery, which keeps `deployColumns` unambiguous without a join. `scanDeployment` takes extra columns.
- **API:** `GET /v1/apps/{app}/deployments?limit=&before=` (`read` scope). `limit` is 1–100 (default 20), and every invalid field is reported at once (422). `next` is the last ID of a full page.
- **Client and CLI:** `shipyard releases APP [--limit N] [--before ID]` prints a table (deployment, status, commit, env revision, created, note), plus the command for the next page.

**Decisions**
- **Keyset, not offset:** a page stays stable while new deployments arrive.
- **The history shows database rows only.** Whether an image is still on the host is the worker's knowledge (invariant 1); rollback availability comes with P3.3.

**Verification** (WSL2, Engine 29.8.1, Caddy 2.11.4, PostgreSQL 18)
- `make lint`, `make test`, `make test-integration`: all ok.
  - `TestReleases`: order, statuses, another app's rows excluded, pages, and two bad cursors.
  - `TestReleasesEndpoint`: the empty list, two pages, and 7 negative cases, plus both fields reported at once.
  - `TestCLIEndToEnd`: `releases` with no history, and `--limit 101` refused.
- **`make test-e2e`: PASS (282 s).** After step 5, `shipyard releases` shows `active failed failed failed superseded`, every row with an env revision, and the active row at the pinned commit. The off-branch commit has no row, since it was refused before a deployment existed.
- No leftovers. Docker tests were not run (no runtime, routing, or build change).

**Next**
- P3.2: `internal/reconcile`.

### 2026-09-29: Phase 2 exit checks and review fixes

- **Phase / task:** Phase 2 exit criteria (PR #19), plus fixes from the CodeRabbit review of the whole stack.
- **Author:** Claude Code (desktop session)

**Done**
- **e2e exit checks:**
  - A client probes the app through Caddy every 50 ms during the health-failing deploy. Every answer was 2xx from the running release.
  - A fault-injected route-verification failure (`PROBE_FAIL_BY_NAME`: `/healthz` fails by hostname only) makes the deploy fail with "switch traffic", and Caddy goes back to the old release. 40 of 207 probes, about 2 s, were answered by the candidate before the restore. That is the known "briefly on candidate" window, and it lasts as long as the verification retries.
- **Review fixes** (each bug fix started with a failing test):
  - `RequeueExpired`: when an operation fails on its last attempt, its in-progress deployment now fails in the same statement. Before, the deployment stayed `building` and so on forever, the janitor kept its container, and the app stayed busy.
  - `queue.Hold`: each heartbeat has a deadline at the lease's expiry. A query stuck on a dead connection could keep the work running past the lease, with two owners (invariant 3).
  - `parseTTL`: the day count is bounded to 1–366 before multiplying. `213505d` overflowed and wrapped to about a day.
  - Smaller fixes: an ignored error in the applogs test, the last `Encode` in the applogs server, a context-bound request in `TestServeEndsStreams`, the `/load` label in the state diagram, and the duplicate `CADDY-OPTIONS` and `SYSTEMD-EXEC` tags in SOURCES (merged).
- **Not changed:** `DeleteIdleApp` still allows deleting an app whose superseded container is in its observation window. The store does not know the worker's window, and deleting an app's containers belongs to P3.8 (delete app as an operation). It is recorded in the open risks.

**Verification**
- The three new tests failed before the fixes and pass after: `TestParseTTL` (`213505d`), `TestHoldGivesUpWhenHeartbeatHangs` (a synctest deadlock before), and `TestRequeueExpired` (the deployment stayed `building`).
- `make lint`, `make test`, `make test-integration`: ok, except for a pre-existing flake in `TestLeaseHandover`. It has a 600 ms lease and fails under load: the unmodified `queue.go` failed on a 20-run batch too, and 20 runs with the fix passed. `-race -count=10` on `queue`: ok.
- Exit checks: `make test-docker` ok, `make test-e2e` PASS (207 s).

**Next**
- Owner: confirm that the logs-resume exit criterion means `events`, and merge #1–#19 in order.

### 2026-09-28: P2.8 API published through Caddy

- **Phase / task:** P2.8: the API listens on localhost or a Unix socket only, and is published through Caddy over HTTPS (HTTP/2)
- **Author:** Claude Code (desktop session)
- **Goal:** Operators and CI reach the API at `https://<API hostname>`. The API itself never listens publicly.

**Done**
- **Config:**
  - `Listen` moved to `Common`, since the worker reads it too. It allows loopback or a clean absolute Unix socket path only.
  - `SHIPYARD_API_ALLOW_PUBLIC_LISTEN` is **removed** (the owner's choice).
  - New `SHIPYARD_API_HOSTNAME`, normalized and checked like an app hostname. On the worker with Caddy enabled, it requires a Unix socket in a directory of its own (not `/`, not the admin directory).
- **Edge:** `EdgeSpec.APISocketDir` is bind-mounted **read-only** at the same path. `EnsureEdge` reports a missing directory clearly. The field is `omitempty`, so existing edges keep their spec hash.
- **Worker:** `ensureEdge` mounts the API socket's directory, and `syncRoutes` renders the API route (`unix//<socket>`, only `/v1/*` and `/hooks/github`; the renderer is from P2.2).
- **API:** adding the API hostname as an app domain is a 409 ("reserved for the Shipyard API").
- **systemd and env:**
  - `SHIPYARD_API_LISTEN=unix:/run/shipyard-api/api.sock` moved from the API unit into `shipyard.env`, which both services read. Before, the worker would have seen `127.0.0.1:8080`.
  - The API unit gets `RuntimeDirectoryPreserve=yes`, and the worker starts `After=shipyard-api.service`.

**Decisions** (ADR-0003 dated note)
- **Socket only for publishing:** Caddy runs in a container and cannot reach the host's loopback.
- **A read-only mount is enough:** Linux refuses writes on a read-only mount only for regular files, directories, and symlinks, so connecting to a socket works. e2e confirms it.
- **`RuntimeDirectoryPreserve=yes`:** a recreated `/run/shipyard-api` would leave Caddy's bind mount pointing at the deleted directory `[SYSTEMD-EXEC]`.

**Problems / surprises**
- The shipped env example set `SHIPYARD_API_LISTEN=127.0.0.1:8080`, and only the API unit set the socket, which the worker could not see. It is fixed by the move above.
- **An exit criterion conflicts with ADR-0008:** "`logs --follow` resumes after a dropped connection without losing events" cannot hold for container logs, which have no ids. Only `events` resumes. The owner should confirm the criterion means `events`, or reopen ADR-0008.

**Verification** (WSL2, Engine 29.8.1, Caddy 2.11.4, PostgreSQL 18)
- `make lint` (after a gofmt of `config_test.go`), `make test`, `make test-integration`: all ok.
  - `TestLoadAPIListenValidation`: 4 accepted, 9 refused, and the removed override no longer opens `0.0.0.0`.
  - `TestAPIHostname`: normalization, 7 refusals, and Caddy off.
  - `TestEdgeSpecValidate` (+4), `TestEdgeCreateOptions` (the read-only mount), `TestEdgeSpecHash` (a new field; the old JSON is unchanged).
  - `TestDomainsPolicy`: a 409 for the API hostname in two spellings.
- `make test-docker`: all ok.
- **`make test-e2e`: PASS (210 s).**
  - The API listens only on a Unix socket, and the CLI uses `unix://`.
  - Through Caddy at `https://api.e2e.example`: `/v1/whoami` is 200 over **HTTP/2** (`ProtoMajor` 2); `/readyz` is 404; the events SSE streams to `event: end`.
  - `domain add api.e2e.example` is refused as reserved.
- No leftovers. Secret scan: see the PR.

**Next**
- Phase 2 exit criteria (see Current status), after the owner answers on the logs-resume criterion. Then P3.1.

### 2026-09-28: P2.7b app logs through the worker socket

- **Phase / task:** P2.7b: `logs --follow` with a bounded tail and best-effort secret redaction
- **Author:** Claude Code (desktop session)
- **Goal:** Operators read the running release's output through the API, while the API still never touches Docker.

**Done**
- **ADR-0008** (the owner's choice): the worker serves `GET /logs` on `SHIPYARD_WORKER_SOCKET` (default `/run/shipyard-worker/logs.sock`, mode 0660, shared group), and the API proxies it as SSE.
- **`runtime.StreamLogs`:**
  - reads Docker logs with timestamps, optional follow, and a tail;
  - splits frames into lines per stream, with CRLF trimmed and lines cut at about 16 KiB;
  - stops when the consumer fails.
- **`internal/applogs`:**
  - `Server`: only the active deployment's container, named by the database; secrets are redacted, and nothing is streamed if they cannot be read; NDJSON with a final `end` line.
  - `Client`: 404 becomes `ErrNoDeployment`, an unreachable socket becomes `ErrUnavailable`, and a stream without its end line becomes `ErrUnexpectedEOF`.
- **`app.Redactor`:** every secret value of 6 or more characters, longest first, exact matches only. `secrets.Env.SecretValues` returns only the secret values.
- **API** `GET /v1/apps/{app}/logs?tail=&follow=` (`read` scope): SSE with keepalives and `event: end` with the reason. It answers 404, 503, 502, or 422 as appropriate.
- **Client and CLI:** the SSE reader is shared by events and logs. `shipyard logs APP [--tail N] [--follow|-f]` writes stdout and stderr lines to the matching output, like `docker logs`.
- **Wiring:**
  - the worker service gets `RuntimeDirectory=shipyard-worker` (0750);
  - `make run-api` and `make run-worker` use `.dev/logs.sock`;
  - the env example and the probe (`/say`) are updated.

**Decisions**
- **Redaction lives in the worker,** where secrets are already decrypted for deploys. Only secret values of at least 6 characters are redacted, because shorter ones would mangle normal output.
- **Logs are not resumable** (no SSE `id`). A reconnect starts a fresh tail.
- **Bug fixed while here (invariant 8):** a failed deploy copied the candidate's last output lines into the operation's events **unredacted**. A failing test came first (`TestDeployUnhealthy` saw `token s3cret rejected`). Those lines now go through the same redactor, and are withheld if the secrets cannot be read.
- **Found, not fixed (open risk, and a separate task was offered):** the API user shares the `shipyard` group with the worker, so it can open the Caddy admin socket.

**Verification** (WSL2, Engine 29.8.1, Caddy 2.11.4, PostgreSQL 18)
- `make lint` (after a gofmt of `config.go`), `make test`, `make test-integration`: all ok. `-race -count=5` on `applogs`, `client`, `app`, and `runtime`; `-race -count=3` on `TestAppLogs` and `TestOperationEvents`: ok.
  - Unit tests:
    - `TestLineWriter`: split frames, CRLF, truncation, a line without a timestamp, a double space;
    - `TestLineWriterStops`, `TestRedactor` (6 cases);
    - `TestLogsOverSocket`: over a real Unix socket; redacted; tail and follow; no deployment; bad ids; a tail over the maximum; unreadable secrets stream nothing; no socket;
    - `TestStreamCutShort`, `TestParseTail`, the client's `TestLogs`, `TestWorkerSocket`.
  - Integration: `TestAppLogs` (the SSE body exactly, keepalive while following, a worker cut, and 7 negative cases) and `TestEnvRevisions` with `SecretValues`.
- **`make test-docker`: all ok.** `TestStreamLogs` shows the Engine 29.8.1 facts in `[DK-LOGS]`: an RFC3339Nano timestamp and one space before each line, stdout and stderr apart, `tail=1`, a live line under follow, follow ending when the container stops, and a consumer error.
- **`make test-e2e`: PASS (151 s).** `shipyard logs` through the real API and worker socket shows `greeting is [REDACTED]` (the secret `GREETING`) and the probe's stderr startup line.
- No leftovers. Secret scan: see the PR.

**Next**
- P2.8: the API listens on localhost or a Unix socket only, and is published through Caddy over HTTPS (HTTP/2).

### 2026-09-28: P2.7a operation event stream

- **Phase / task:** P2.7a: operation events as SSE (P2.7 split in two; P2.7b is logs)
- **Author:** Claude Code (desktop session)
- **Goal:** Operators and CI watch a deploy live, and a dropped connection loses nothing.

**Done**
- **API** `GET /v1/operations/{id}/events` (`read` scope):
  - `id` = `seq` with JSON data; `retry: 2000`; a keepalive comment every 15 s.
  - `Last-Event-ID` resume, and 422 if it is malformed.
  - `event: end` with the operation once it has finished and two polls came back empty.
- **Serve:** a `stopping` channel, closed by `RegisterOnShutdown` and passed in through `BaseContext`, ends open streams at shutdown. Ordinary requests still finish.
- **Client:** `FollowEvents` parses SSE and reconnects with `Last-Event-ID`. A 45 s idle timeout drops a dead connection. It gives up after 5 failed reconnects in a row; any event or keepalive resets that count.
- **CLI:** `events ID`, and `deploy --follow` (exit 1 unless the operation succeeded).

**Decisions**
- **Split P2.7** (CLAUDE.md §2: each half is about 400 lines). For logs, the owner chose **a worker log socket that the API proxies** over copying logs into PostgreSQL. It changes a boundary, so P2.7b starts with an ADR.
- **Poll, not LISTEN/NOTIFY:** one indexed query per stream every 500 ms, which is simple and negligible for a handful of operators.
- **End after two empty polls:** `activate` appends "is active" and the drain plan after its commit, so ending at the first finished poll would drop them. The e2e run shows both arrive.

**Verification** (WSL2, Engine 29.8.1, Caddy 2.11.4, PostgreSQL 18)
- `make lint` (after a gofmt of `api_test.go`), `make test`, `make test-integration`: all ok. `-race -count=5` on `api` and `client`; `-race -count=3` on the stream and CLI integration tests: ok.
  - `TestOperationEventsStream`: headers, the retry line, multi-line messages, a keepalive, a live event, the end on cancellation, two resumes, and 5 negative cases (422 ×2, 404 ×2, 401).
  - `TestServeEndsStreams`: shutdown returns at once while a stream is open, and an in-flight request still completes.
  - `TestFollowEventsResumes`: data split across lines, a cut mid-event, and a resume at `Last-Event-ID: 2`. `TestFollowEventsErrors`: 404 is final, and a server that keeps failing is given up after 6 connections.
  - `TestCLIEndToEnd`: `events` prints aligned multi-line events and fails with "cancelled: superseded by".
- `make test-docker`: all ok.
- **`make test-e2e`: PASS (122 s).** Deploy 5 runs with `deploy --follow`. Its output has the health check, the Caddy verification, "is active", the drain plan, and "succeeded".
- No leftovers. Secret scan: see the PR.

**Next**
- Write the ADR for the worker log socket (path, mode, who connects, what it serves). Then P2.7b: a bounded tail, `--follow`, and best-effort redaction of the app's secret values.

### 2026-09-28: P2.6 observation window and drain

- **Phase / task:** P2.6: observation window, then graceful stop of the previous container with the per-app `stop_timeout`
- **Author:** Claude Code (desktop session)
- **Goal:** Keep the previous release running after a switch, then stop it gracefully, driven by the database so a worker restart loses nothing.

**Done**
- **Deploy:** activation no longer stops the previous container. It logs the window and the app's stop timeout to the operation's events.
- **`app.Janitor`** (Reconciler step 2, run after the Caddy sync), with the database deciding:
  - a `superseded` deployment past `ended_at` + window: `docker stop` with the app's `stop_timeout`, then removal;
  - `failed`/`cancelled`: removed;
  - active, in-progress, or within the window: kept.
  - It carries on past a failing container. A database error removes nothing.
- **`runtime.ListManaged`:** app containers by the `io.shipyard.managed` label, running or not. It skips containers with an invalid slug or deployment label, and the edge.
- **Config:** `SHIPYARD_OBSERVATION_WINDOW` (default 5m, 0s–24h, 0 allowed).

**Decisions**
- **A container whose deployment is not in this database is never removed.** My first draft removed it as an orphan. The e2e case showed that an e2e or test worker would then delete the owner's development containers on the same engine. Cost: a deleted app's containers stay (app delete cascades its deployments). This is recorded as an open risk, and the fix is an installation label.
- **The drain lives in the reconciler, not the deploy.** A 5-minute wait inside the operation would hold the app's operation slot and the lease. No schema change: `ended_at` is the window's start.

**Verification** (WSL2, Engine 29.8.1, Caddy 2.11.4, PostgreSQL 18)
- `make lint` (after a gofmt of `config.go` and `janitor_test.go`), `make test`, `make test-integration`: all ok. `-race -count=5` on `app` and `config`: ok.
  - `TestJanitorSweep` (9 containers: drain at and past the window, kept within it, failed removed, foreign kept, exited not stopped again), `TestJanitorCarriesOn`, `TestLoadWorkerObservationWindow` (5 valid, 3 invalid), and the deploy tests updated (no stop/remove at activation; the event is logged).
- `make test-docker`: all ok, including the new `TestListManaged` (a running and a created container listed, a forged label skipped).
- **`make test-e2e`: PASS (134 s).** With a 6 s window, the first container is still running right after deploy 5 succeeds, then removed by the reconciler within 30 s. Both hostnames serve the second container.
- No leftovers. Secret scan: clean (see the PR).

**Next**
- The owner merges #1–#15 in order.
- P2.7: SSE for operation events (`id`, `Last-Event-ID`) and logs.

### 2026-09-28: P2.5 domain API

- **Phase / task:** P2.5: domain API
- **Author:** Claude Code (desktop session)
- **Goal:** Operators add and remove hostnames through the API and CLI, safely (DNS preflight, allow-list, uniqueness), and Caddy follows without the API touching it (invariant 1).

**Done**
- **`internal/app`:**
  - `NormalizeHostname`: lowercase, trailing dot removed, exact FQDN; no wildcards, IPs, or single labels.
  - `SuffixAllowed`: matches at a label boundary.
  - `PointsHere`: every record must be ours, at least one record, v4-mapped addresses unmapped.
  - `ContainerName`/`Upstream`: the deterministic convention. A worker test keeps it equal to `runtime.ContainerName`.
- **API `/v1/apps/{app}/domains`** (list: read; add and remove: admin):
  - normalize → allow-list (422) → preflight (NXDOMAIN or no records 422, a foreign record 422 naming it, lookup failure 503, preflight on without IPs 503) → `CreateRoute`;
  - a duplicate is 409 "already used by an app".
- **Store:**
  - `CreateRoute`, under the app lock: it targets the active deployment, using the upstream its other routes use, or else the convention.
  - `RoutesByApp`, `DeleteRoute`.
  - `ActivateDeployment` also moves routes on the superseded deployment, or on none.
- **Config:** `SHIPYARD_PUBLIC_IPS` (public only), `SHIPYARD_DOMAIN_SUFFIXES`, `SHIPYARD_DNS_PREFLIGHT`, and on the worker `SHIPYARD_RECONCILE_INTERVAL`.
- **Worker:** the reconciler now also runs `Router.Sync` every interval. A deploy calls `Release` after commit or failure, so followers load right away.
- **Client/CLI:** `domain add|remove|list`. The Makefile's `run-api` turns the preflight off.

**Decisions** (dated note in ADR-0003; ARCHITECTURE §6/§7)
- **Several hostnames per app**, where ARCHITECTURE v2 sketched `PUT …/domain` (one). The schema allows it, and one app per hostname still holds.
- **A hostname added to a serving app targets the running deployment at once**, by convention. The alternative, a new operation kind with its own verification, needs a migration for little gain in the MVP.
- **Route changes reach Caddy through the periodic reconcile** (default 60 s) plus a sync after each activation. No LISTEN/NOTIFY yet.

**Problems / surprises**
- **I found a race in my own P2.4 code while adding the periodic sync.**
  - A `Sync` during a switch's verification would revert Caddy to the old container, and the check would then pass against it (invariant 5).
  - Fix: `routing.Router` keeps pending switches in every render until the deploy's `Release`.
  - `TestRouterSyncKeepsPendingSwitch` covers it: the failed switch stays through `Sync` until `Release`.
  - The app port changed from `Restore` to `Release(app)`.
- `TestActivateMovesVerifiedRoutes` from P2.4 expected an unverified route with no deployment to keep its target. With followers it now follows the app; the test was updated on purpose.

**Verification** (WSL2, Engine 29.8.1, Caddy 2.11.4, PostgreSQL 18)
- `make lint` (after a gofmt of `config.go`), `make test`, `make test-integration`: all ok. `-race -count=5` on `app` and `routing`.
  - Unit: hostname normalization (5 valid, 13 invalid), suffixes, `PointsHere` (7 cases), config (6 negative cases), the router regression test, and the container names agreeing.
  - Integration:
    - `TestDomainsAPI`: normalization, 403 for read tokens, 409, five 422 cases including a stray AAAA, 503 when DNS is down, list, delete, and a second delete (404).
    - `TestDomainsPolicy`: allow-list; preflight on without IPs is 503; preflight off does no lookup.
    - `TestDomainOnActiveDeployment`: the upstream is `shipyard-web-<dep>:3000`.
    - `TestCreateRoute`, the updated `TestActivateMovesVerifiedRoutes`, and `TestCLIEndToEnd` with the domain commands.
- `make test-docker` (all packages): all ok.
- **`make test-e2e`: PASS (106 s):**
  - the first hostname is added with `domain add` before the deploy;
  - **a second is added while the app serves** ("serving deployment"), and **Caddy serves it from the first container** after the reconcile;
  - after deploy 5, **both hostnames serve the second container**;
  - `domain remove` → Caddy stops routing it.
- No leftovers. Secret scan: see the PR.

**Next**
- The owner merges #1–#14 in order.
- P2.6: the observation window, then a graceful stop of the previous container.

### 2026-09-28: P2.4 traffic switching

- **Phase / task:** P2.4: the `switching` phase
- **Author:** Claude Code (desktop session)
- **Goal:** A healthy candidate takes over the app's hostnames only after Caddy verifiably routes to it, and any failure restores the previous routes (invariant 5, ARCHITECTURE §5 step 7).

**Done**
- **The renderer's `Settings.VerifySocket`** adds a `verify` server: the same routes, plain HTTP, on `caddy-verify.sock|0660`, with `automatic_https.skip` for all hosts. That shape comes from `caddy adapt`. There are two new golden files.
- **`routing.Router`:**
  - `Switch(appID, upstream, healthPath)` renders the table with that app's routes on the candidate, applies it, then requests `health_path` for each hostname through the verify socket: 5 attempts 0.5 s apart, 2xx/3xx, no redirects or proxy. It returns the verified hostnames; with none, it loads nothing.
  - `Restore` re-renders the table as committed. The worker's startup sync now uses it.
- **Store:**
  - `MarkSwitching`, and `switching_at` read back.
  - `ActivateDeployment(…, upstream, hostnames)` moves **only the verified hostnames**, in the activation transaction. A verified route that vanished returns `ErrConflict` and rolls everything back.
- **`internal/app` (`switchTraffic`):**
  - The `switch` phase runs after the health gate. The upstream is `<container name>:<port>`, since `runtime.State` now carries the name.
  - **Restore comes before removing the candidate** on failure. A lost lease or shutdown also restores, but records nothing.
  - With no routes, it logs "no routes yet".
- **Worker:** one `Router` for both the startup sync and deploys. `make test-docker` now runs `-p 1` (see below).

**Decisions** (dated note in ADR-0003; ARCHITECTURE §5)
- **Verify on a private plain-HTTP Caddy listener, not over public HTTPS.** Routing is proven through Caddy by `Host` header, with no dependence on ACME timing for a first deploy.
- Commit only verified hostnames.

**Verification** (WSL2, Engine 29.8.1, Caddy 2.11.4)
- `make lint`, `make test`, `make test-integration`: all ok. `-race -count=5` on `app` and `routing`: ok.
  - Unit: 5 new use-case tests (switch, switch failure restores before remove, activation failure restores, lost lease after the switch restores but records nothing, no routes), 4 Router tests against a fake Caddy whose verify server answers from the loaded config, and 2 new rejection cases.
  - Integration: `TestActivateMovesVerifiedRoutes` (only verified and own routes move; a vanished route aborts the commit, leaving the deployment not active and the operation running).
- **`make test-docker` (all packages, `-p 1`): all ok.** `TestRouterAgainstCaddy` with the real Caddy and v1/v2 upstreams on an app network:
  - the verify socket is 0660;
  - v1, then after `Switch` **v2 through both the verify socket and public HTTPS**, then v1 again after `Restore`;
  - an unreachable candidate → `status 502` after 3.5 s, and v1 again after `Restore`.
- **`make test-e2e`: PASS (105 s).** A route row is added before the first deploy, and **HTTPS through the worker's Caddy** reads the serving container's `/etc/hostname`:
  - the first container after deploy 1;
  - still the first after the broken, off-branch, and unhealthy deploys;
  - the second after deploy 5;
  - the injected secret env through Caddy.
- No leftovers.

**Problems / surprises**
- **Parallel docker test packages interfered.** Each package's edge joins every app network on the host, as the one production edge must. With `routing` and `runtime` run together, a network got a foreign edge endpoint, so a network removal and an edge `docker restart` failed and one test network was left behind (removed afterwards).
  - Alone, the edge tests pass twice in a row.
  - Fix: `make test-docker` uses `-p 1`. Nothing changes in production.

**Next**
- The owner merges #1–#13 in order (retarget each to `main`, merge commits).
- P2.5: the domain API.

### 2026-09-28: P2.3 Caddy admin client

- **Phase / task:** P2.3: admin socket client
- **Author:** Claude Code (desktop session)
- **Goal:** Replace Caddy's config safely over the admin socket: read with an `Etag`, write conditionally, and handle 412 and rejections (ADR-0003, invariants 5 and 12).

**Done**
- **`routing.Admin`** (Unix socket only, no proxy, bounded responses, a 1-minute default deadline):
  - `Config` returns the body and `Etag`, and requires the `Etag`.
  - `Load(cfg, etag)` is a whole replace via **`POST /config/`** with `If-Match`. A 412 is `ErrConflict`; any other non-200 is a `LoadError{Status, Message}`, with Caddy's `{"error"}` extracted and bounded.
  - `Apply(cfg)` reads, compares semantically (Caddy re-encodes its stored config), and reloads only if different. After a 412 it re-reads and retries, up to 3 attempts.
- **Worker:** after `EnsureEdge`, `syncRoutes` runs `ListRoutes` → `Render` → `Apply` (reconciler step 3, at start). New config: `SHIPYARD_CADDY_CA` (``, `staging`, `internal`) and `SHIPYARD_ACME_EMAIL`. `make run-worker` and the e2e test use `internal`.

**Problems / surprises → decision**
- **`POST /load` ignores `If-Match`**, contradicting ADR-0003, SOURCES, ARCHITECTURE, and CLAUDE.md.
  - Found by the first real-Caddy run: a load with a stale `Etag` succeeded.
  - Confirmed in the source of Caddy v2.11.4: `handleLoad` never reads the header. Only `/config/…` requests (`handleConfig` → `changeConfig`) check it, returning 412.
  - Stopped per CLAUDE.md §9, recorded `CADDY-ADMIN-SRC`, and corrected `CADDY-API`, ADR-0003 (dated note; the decision is unchanged), ARCHITECTURE §3/§5, ROADMAP, and CLAUDE.md §4.
  - Then `Load` moved to `POST /config/`: the same replace, no-op-if-unchanged, and rollback (`changeConfig`), plus the 412.
- A rejected config returns **500** via `/config/` (400 via `/load`).
- The real-Caddy test keeps a check that `/load` still ignores `If-Match`, so a Caddy upgrade that changes this is noticed.

**Verification** (WSL2, Caddy 2.11.4)
- `make lint`, `make test`, `make test-integration`: all ok. 10 unit tests against a fake Caddy on a real Unix socket: Etag, If-Match, no reload when equal, a 412 retry with a fresh Etag, giving up after 3, the rejection message, a stale Etag, a missing socket, and context cancel. `-race -count=5`: ok.
- **`TestAdminAgainstCaddy` (docker)**, against the real P2.1 edge:
  - the Etag has the form `"/config/ …"`;
  - the first `Apply` reloads and the second does not;
  - **`Load` with a stale Etag → `ErrConflict`, and the config is unchanged**;
  - `/load` with a stale `If-Match` → 200 (the vendor fact);
  - a broken config → `LoadError` 500 "unknown module", and **the previous config keeps running**.
- `TestRenderedConfigServes` still passes, and **`make test-e2e` passes (217 s)**: the worker starts with `syncRoutes`.
  - This run was slower than earlier ones (111 s), and the real-Caddy test took 42 s instead of 6 s; nothing failed.
- No leftovers.
- **Merging PRs #1–#11** (requested by the owner) was **blocked by the permission classifier**. No PR was changed; all are still open with their original bases.

**Next**
- The owner merges #1–#12 in order. Each PR's base must be retargeted to `main` before its merge; use merge commits, not squash.
- Then P2.4: the `switching` phase.

### 2026-09-27: P2.2 route renderer

- **Phase / task:** P2.2: `internal/routing` renderer
- **Author:** Claude Code (desktop session)
- **Goal:** Caddy's complete config as a pure function of the `routes` table (invariant 2, ADR-0003).

**Done**
- **`routing.Render(Settings, []Route) ([]byte, error)`:**
  - `admin.listen` on the edge socket (`|0660`), and one server `shipyard` on `:443`.
  - The optional API hostname proxies only `/v1/*` and `/hooks/github`; anything else there is 404.
  - One terminal route per hostname: `reverse_proxy` to its upstream, or `503 no active deployment`.
  - Issuers: default, Let's Encrypt with an email, LE staging, or internal.
  - Output is sorted by hostname, so it is byte-for-byte deterministic, and the caller's slice is never modified.
- **Validation refuses to render anything** for invalid input:
  - hostnames that are not the schema's lowercase FQDN form (no wildcards or placeholders), duplicates, or an app on the API hostname;
  - upstreams other than `host:port` with a real port (no network prefixes, port ranges, placeholders, or sockets for apps);
  - an unclean admin socket path, an unknown CA, or a bad email.
- `store.ListRoutes` (ordered by hostname) and `routing.FromStore`.

**Decisions**
- **The JSON shape comes from Caddy itself** (`caddy adapt` of an equivalent Caddyfile on 2.11.4), because the JSON docs pages render client-side `[CADDY-JSON]`.
- **Apps can never proxy to a Unix socket.** Only the API upstream may be `unix//…`, so a route row cannot point Caddy at, say, the Docker socket.
- **Settings passed in, not new config.** The API hostname, the API upstream, the CA, and the email come in as `Settings`. Wiring them from config comes with the load path (P2.3/P2.4) and P2.8.

**Verification** (WSL2, Engine 29.8.1, Caddy 2.11.4)
- `make lint`, `make test`, `make test-integration`: all ok.
  - Unit: 5 golden files, determinism, admin socket kept, `FromStore`, and 19 rejection cases.
  - Integration: `TestListRoutes`, covering order, and upstream and deployment present or absent.
- **`TestRenderedConfigServes` (docker, 7 s):** the empty config loads into the real P2.1 edge. Then the full config loads, with the internal CA and a `caddy respond` upstream on an app network:
  - `web` → 200 "app ok", `idle` → 503 "no active deployment";
  - `api/v1/whoami` and `api/hooks/github` → proxied, and `api/admin` and `api/v1` → 404;
  - an unrouted hostname fails the TLS handshake;
  - `http://` → 308 to `https://`;
  - the admin socket still answers after the load.
- No leftovers.

**Next**
- P2.3: the admin socket client: `GET /config/` with `Etag`, `POST /load` with `If-Match`, and handling of 412 and other errors.

### 2026-09-27: P2.1 Caddy edge

- **Phase / task:** P2.1: Caddy container bootstrap
- **Author:** Claude Code (desktop session)
- **Goal:** One Caddy container that alone publishes host ports, joins every app network, and exposes its admin API only on a permissioned Unix socket (ADR-0003, invariants 11 and 12).

**Done**
- **`runtime.EnsureEdge(EdgeSpec)`** (idempotent) prepares the admin directory, then ensures the edge network, the `-data` and `-config` volumes, and the container (pulled if missing). It then starts it, joins every app network, and waits until the socket accepts connections.
  - A spec-hash label recreates the container when the spec changes, keeping the volumes.
  - A foreign container or network with the name is refused.
- **The container** (one place, `EdgeSpec.createOptions`):
  - `caddy:2.11.4-alpine@sha256:6aeddd44…`, running `caddy run --resume`, with `CADDY_ADMIN=unix/<dir>/caddy-admin.sock|0660`;
  - user `0:<worker gid>`, `--cap-drop ALL` plus `NET_BIND_SERVICE`, `no-new-privileges`, and a read-only rootfs with a `/tmp` tmpfs;
  - 512 MiB, 1 CPU, 512 pids, the `local` log driver, `unless-stopped`;
  - ports 80/tcp, 443/tcp, and 443/udp;
  - the socket directory is the only bind mount.
- **Admin directory:** the worker makes it group = its own gid with mode 2770 (setgid), or checks that an existing directory is set up that way.
- **App networks:** `Runtime.Edge` makes `EnsureNetwork` (and so `Create`) join the edge to new app networks. `RemoveNetwork` detaches the edge first. `AttachEdge` and `RemoveEdge` complete the set.
- **Worker:** calls `EnsureEdge` at start. Config: `SHIPYARD_CADDY[_NAME|_IMAGE|_ADMIN_DIR|_BIND|_HTTP_PORT|_HTTPS_PORT]`. `make run-worker` publishes only on 127.0.0.1:18081/18443.

**Decisions** (dated note in ADR-0003; ARCHITECTURE §7)
- The socket gets its own directory, `/run/shipyard/caddy/`, so Caddy never sees the API socket in `/run/shipyard`.
- **Root with the worker's gid**, not a non-root user:
  - binding 80/443 then needs only `NET_BIND_SERVICE`;
  - the socket can be created in the group-writable directory without `CAP_DAC_OVERRIDE`;
  - `no-new-privileges` would block the file capability for a non-root user.
- `--resume` plus the `/config` volume keeps serving after restarts. P2.2 must render `admin.listen` on the socket, because a loaded config overrides `CADDY_ADMIN`.

**Verification** (WSL2, Engine 29.8.1)
- `make lint`, `make test`, `make test-integration`: all ok. Unit: `TestEdgeSpecValidate` (11 negative cases), `TestEdgeCreateOptions`, `TestEdgeSpecHash`, `TestLoadWorkerCaddy` (8 negative cases).
- Docker, `internal/runtime` (16 tests, 31 s):
  - **`TestEdgeBootstrap`:**
    - the socket is a socket, mode 0660, with the worker's gid;
    - `GET /config/` over the socket gives `null`;
    - **TCP 2019 on the container IP is refused**;
    - hardening is confirmed by inspect, and the only bind mount is the socket directory.
  - After `POST /load` of a static response, it is served through the published loopback port.
  - A second `EnsureEdge` returns the same ID.
  - **`docker restart` resumes the loaded config**, and a spec change recreates the container with the config kept.
  - `TestEdgeJoinsAppNetworks`: networks both before and after `EnsureEdge` are joined, and `RemoveNetwork` detaches.
  - `TestEdgeRefusesForeignContainer`.
- **Mutation checks:**
  - socket `|0666` → the test fails on the mode;
  - no `NET_BIND_SERVICE` → Caddy never comes up.
- `make test-e2e`: PASS (111 s). The worker now starts its own Caddy, which joins the app network, and the app container publishes no ports.
- No leftovers: only the pre-existing dev PostgreSQL.

**Problems / surprises**
- **The official image's `caddy` binary has `cap_net_bind_service=ep`.** Without that capability, the exec itself fails: `exec /usr/bin/caddy: operation not permitted` (recorded in `CADDY-IMAGE`).
- **Engine 29 reports `CapAdd` as `CAP_NET_BIND_SERVICE`**, while the request says `NET_BIND_SERVICE` (recorded in `MOBY-CLIENT`).

**Next**
- P2.2: `internal/routing`: render the full Caddy JSON from `routes` (with `admin.listen` on the socket, the API, and `/hooks/github`); golden-file tests.

### 2026-09-27: P1.11 deploy worker

- **Phase / task:** P1.11: the deploy use case, the health probe, and the worker loop
- **Author:** Claude Code (desktop session)
- **Goal:** `shipyard deploy` really runs: fetch → build → start → health → active, with each phase persisted before its side effect and a crash resuming where it stopped.

**Done**
- **Store (`deployments.go`):**
  - `CreateDeployment`, `DeploymentByOperation`/`ByID`, `ActiveDeployment`, `RecordImage`, `RecordContainer`, `MarkHealthChecking`, `FailDeployment`.
  - `ActivateDeployment` supersedes the old active, marks this one active, and completes the operation in one transaction.
  - **Every write joins the operation's lease** (`lease_owner`, `running`); zero rows is `ErrLeaseLost`.
- **`internal/app/deploy.go` (`Deployer.Run`):**
  - The operation phases are `fetch`, `build`, `start`, `health`, `activate`.
  - The deployment row is created after the verified fetch and pins the latest env revision.
  - The container ID is persisted before start.
  - The health gate requires the container to stay running without restarts.
  - Activation then drains the previous container.
  - **Resume:** a recorded image skips fetch and build, the commit stays pinned, and the container is re-created idempotently.
  - **Failure:** final. It captures the candidate's last 50 lines, removes the candidate, and fails the deployment and the operation.
  - A lost lease or shutdown records nothing, so the next owner resumes.
- **`internal/health`:** 3 consecutive 2xx/3xx, 1 s interval, 2 s per request, no proxy, no redirects. A dead container stops the gate at once, and a caller's cancel cause (lost lease) is passed through.
- **Worker:**
  - The queue loop with `Hold` (lease heartbeats), plus `RequeueExpired` at start and every lease.
  - Adapters for source, build, and runtime.
  - `Ensure` on the builder at start.
  - New config: `SHIPYARD_WORK_DIR`, `SHIPYARD_SOURCE_BASE_URL`, `SHIPYARD_BUILDER[_MEMORY|_CPUS]`. The KEK is now required.
- `runtime.Logs` (bounded, demultiplexed). The API uses `app.DeployPayload`.
- **`test/e2e` + `make test-e2e`:** real binaries, a local git HTTP server, and the CLI (details under Verification).

**Decisions** (no ADR; within ARCHITECTURE §5)
- **Ports live in `internal/app`; adapters live in `cmd/shipyard-worker`.** `internal/source` imports `internal/app` (a cycle otherwise), and the API must not link the Docker client.
- **No automatic retry of deploy failures.** Only lease loss or shutdown leads to a retry.
- With no route yet, the previous container is drained right away, with no observation window.
- The e2e test uses its own builder name (`SHIPYARD_BUILDER`), so it never touches the owner's `shipyard` builder.

**Verification** (WSL2: Engine 29.8.1, buildx 0.37.1, git 2.43.0, PostgreSQL 18)
- `make lint` (including the `e2e` tag): exit 0. `make test`, `make test-integration`, and `make test-docker`: all ok. `-race -count=5` on `app` and `health`: ok. `go mod verify`: ok.
- Unit tests: 11 use-case scenarios (happy path, pinned ref, fetch, build, unhealthy, 4 container deaths, resume after and before the build, lost lease, resumed failure, bad payload), 7 health cases, and 3 store integration tests (lifecycle, lease guard including requeue, failure).
- **`make test-e2e`: PASS (107 s)**, covering every Phase 1 exit criterion:
  1. A pinned SHA deploys, and an `Idempotency-Key` replay returns the same operation. The container runs the pinned commit, and `docker inspect` shows `["ALL"] ["no-new-privileges"] local 536870912 1000000000 512 false {}`. The secret env value is readable inside.
  2. A broken Dockerfile gives `failed` with `missing-file` in the error.
  3. A SHA off the branch gives `failed`, "commit is not on the tracked branch".
  4. An unhealthy release gives `failed` with `status 500`, and the same container is still the only one running.
  5. A healthy release replaces it, and the old container is removed.
- No leftovers: no containers, networks, `shipyard*` images, or extra builders.

**Problems / surprises**
- **A partial clone downloads off-branch commits** `[GIT-PARTIAL]`.
  - In the e2e test, a feature-branch SHA got past `cat-file -e`: git lazily fetched it from the server. Only `merge-base` refused it, so invariant 7 held, but ARCHITECTURE's promise that nothing else is fetched was false.
  - Confirmed with a standalone script, and in git's docs.
  - Fix: `cat-file` and `merge-base` run with `GIT_NO_LAZY_FETCH=1`.
  - Failing-first: the extended `TestFetchRejectsCommitsOffBranch` fails on the old code ("the feature-branch commit is in the workspace") and passes on the new.
- **The e2e test first failed on a race in the test itself.** The operation succeeds at activation, and draining the old container comes after, so the test now waits for the drain. Failure warnings are now also logged by the worker, not only stored as events.

**Next**
- The owner merges #1–#9 in order. Then P2.1: the Caddy container bootstrap.

### 2026-09-27: P1.10 container runtime

- **Phase / task:** P1.10: `internal/runtime`
- **Author:** Claude Code (desktop session)
- **Goal:** Run a deployment's image as a hardened container on its app's own network, safely repeatable after a crash (invariants 3 and 10).

**Done**
- `EnsureNetwork` / `RemoveNetwork`: the bridge network `shipyard-app-<slug>`, labelled `io.shipyard.{managed,app}`. Both are idempotent, and both refuse a same-named network without those labels.
- `Create(Spec)` returns the ID without starting, so the caller persists it first.
  - It ensures the network, then creates `shipyard-<slug>-<deployment-id>` from the image ID.
  - Labels are `io.shipyard.{managed,app,deployment,commit}`. The env is injected as sorted `KEY=value`.
  - A retry finds the existing container and returns it only if deployment and image match; otherwise `ErrNameTaken`.
- `Start`, `Inspect` (status, restarts, OOM, exit code, IP on the app network), `Stop` (SIGTERM, then SIGKILL after the rounded-up timeout), `Remove` (force, anonymous volumes).
  - All are idempotent, with `ErrNotFound` for a missing container.
  - All refuse unlabelled containers (`ErrNotManaged`).
- The hardened set is built in one function (ARCHITECTURE §7): `CapDrop ALL` plus an allowlist, `no-new-privileges`, `Memory`, `NanoCPUs`, `PidsLimit`, `unless-stopped`, the `local` log driver, one network, and no ports, mounts, or host namespaces. `Spec` has no field for anything forbidden.
- `Spec.Validate` checks everything; its errors name env keys, never values (invariant 8).

**Changed files**
- `internal/runtime/`: the package, unit tests (CI), `docker`-tagged tests, and the `testdata/probe` helper.
- `go.mod`/`go.sum`: new dependency, `github.com/moby/moby/client` v0.6.0, pulling in `moby/moby/api` v1.56.0 and `containerd/errdefs` v1.0.0. It is the approved Docker client (CLAUDE.md §4); shelling out to the CLI, as `build` does, would give no typed inspect data. It also pulls in OpenTelemetry HTTP instrumentation, which exports nothing unless configured.
- ARCHITECTURE §7, SOURCES (`MOBY-CLIENT`), ROADMAP, DEVELOPMENT.

**Decisions** (no ADR; these apply ARCHITECTURE §7)
- **Capability allowlist:** `CHOWN DAC_OVERRIDE FOWNER NET_BIND_SERVICE SETGID SETUID`. **Pids default:** 512. Neither has a per-app column yet; add one when an app needs it.
- **`Create` calls `EnsureNetwork`**, and every lifecycle call checks the labels, so the worker cannot hit a foreign container even when given a wrong ID.

**Verification** (WSL2: Engine 29.8.1, API 1.56, cgroup v2)
- `make lint`: exit 0. `make test` and `make test-integration`: all ok. `go mod verify`: all modules verified.
- `make test-docker`: all ok. `internal/runtime` has 6 Docker tests, 23 s.
  - **Exit criterion (`TestHardenedContainer`)**: the API inspect and `docker inspect` both show `["ALL"] ["no-new-privileges"] local 67108864 500000000 64 false false`. `PortBindings` is empty, with no bindings even though the image `EXPOSE`s 8080. Exactly one network.
  - **From inside the container:** `NoNewPrivs: 1` and `CapPrm/CapEff/CapBnd` all zero; `pids.max=64`, `memory.max=67108864`, `cpu.max=50000 100000`; the injected env is readable, including an empty value.
  - An allowlisted `NET_BIND_SERVICE` gives `CapBnd 0x400` exactly.
  - **Idempotency:** create is idempotent and gives `ErrNameTaken` for another image; a missing image gives `ErrNotFound`. Start, stop, and remove are idempotent, with `ErrNotFound` after removal.
  - **Label guard:** a foreign network or container is refused by every call and survives.
- **Mutation check:** removing `no-new-privileges` and `CapDrop` makes `TestHardenedContainer` fail on 4 assertions.
- No leftovers after the tests: no `io.shipyard` containers, `shipyard-app-*` networks, or `shipyard-test/*` images.

**Problems / surprises**
- Engine 29.8.1 **creates a container on a network that does not exist** and fails only at start. The first test run caught this, so `Create` now ensures the network (recorded in `MOBY-CLIENT`).
- The WSL daemon's default log driver is `json-file`, so the per-container `local` setting is required, not redundant.

**Next**
- P1.11: the deploy use case: fetch → build → `runtime.Create` (persist the container ID) → `Start` → health probe on `State.IP:internal_port`, with phases persisted before each side effect.

### 2026-09-27: P1.9 image build

- **Phase / task:** P1.9: `internal/build`
- **Author:** Claude Code (desktop session)
- **Goal:** Build a verified checkout into a local image on a resource-limited BuildKit builder (ADR-0004).

**Done**
- `Builder.Ensure(Limits{Memory, CPUQuota})`: creates the `docker-container` builder with `--driver-opt memory=…,cpu-quota=…,cpu-period=100000` if missing, then bootstraps it `[DK-BX-CONTAINER]`. `Remove`.
- `Builder.Build(Request)`:
  - `buildx build --builder shipyard --load --provenance=false --sbom=false --progress=plain --metadata-file … --tag shipyard/<slug>:<sha12>`, plus the `io.shipyard.{managed,app,commit,deployment}` labels.
  - A deadline (15 min default); on expiry the CLI gets SIGINT so BuildKit cancels.
  - One build at a time.
  - Result: `ImageID` from `image inspect`, and the metadata file.
- The docker CLI gets a whitelisted environment, so no `SHIPYARD_*` variable reaches a build `[DK-BUILD-SECRETS]`.
- Bounded log capture: at most `MaxLog` bytes (5 MB default) go to the sink, then one truncation notice. The last 20 lines always end up in the failure error.

**Decisions**
- **Attestations are off (`--provenance=false --sbom=false`)**, because `--load` of an attested image fails on the classic image store `[DK-ATTEST]`. The image ID is the identity, and the metadata file (which includes buildx's own provenance) is kept.
- **Docker tests get their own build tag, `docker`, and `make test-docker`.** They run on the owner's machine (CLAUDE.md §6); CI only compiles them through `make lint`, which now vets and staticchecks the `docker` tag too.
- The sink is a callback. The worker (P1.11) connects it to `AppendOperationEvent`.

**Verification** (WSL2: Engine 29.8.1, buildx 0.37.1)
- `make lint`: exit 0. `make test` and `make test-integration`: all ok.
- `make test-docker` (8 tests, 40 s):
  - **the builder container really has Memory=512 MiB, CpuQuota=100000, CpuPeriod=100000**, and `Ensure` is idempotent;
  - a build gives `sha256:` ID, the tag, all four labels on the image ID, a captured log, and `ImageID == containerimage.digest` on the containerd store;
  - a broken Dockerfile (`COPY missing-file`) returns `ErrBuildFailed` naming the missing file, with the log within the 400-byte budget;
  - a 1 ms deadline returns `deadline of 1ms exceeded`.
- After the tests, `docker buildx ls` shows only `default` and there are 0 `shipyard/web` images: no leftovers.

**Problems / surprises**
- **A documented fact did not hold.** With `--load`, buildx 0.37.1 writes `containerimage.digest` and `containerimage.descriptor` but **no `containerimage.config.digest`**, which SOURCES, ARCHITECTURE, and ADR-0004 expected. I recorded the observation in `DK-BX-BUILD`, updated ARCHITECTURE §4, and added a dated note to ADR-0004 (the decision is unchanged, and the whole file is persisted).

**Next**
- P1.10: `internal/runtime` (moby client, per-app network, hardened flags, and the automated `docker inspect` checks from the exit criteria).

### 2026-09-27: P1.8 source fetch

- **Phase / task:** P1.8: `internal/source`
- **Author:** Claude Code (desktop session)
- **Goal:** Put the exact commit into a fresh per-operation workspace, and prove it is on the tracked branch (invariant 7).

**Done**
- `Fetcher.Fetch(Request{OperationID, Repo, Branch, Ref, Token})` returns `Checkout{Dir, SHA, BranchHead}`:
  1. Validate every input first (UUID, owner/name, git ref rules plus no leading `-`, full SHA).
  2. Empty `<Root>/op-<id>` (a retry starts clean); the root is mode 0700.
  3. `git clone --no-checkout --filter=blob:none --single-branch --no-tags --branch B -- URL DIR`.
  4. Resolve the branch head; the target is `Ref` or the head.
  5. `cat-file -e` in the branch history (otherwise `ErrNotOnBranch`, fetching nothing more), then `merge-base --is-ancestor` `[GIT-MERGE-BASE]`.
  6. Detached checkout, and verify that `HEAD` equals the SHA.
- git runs via `exec` (no shell) with a locked-down environment:
  - no host git config, no prompts, `core.hooksPath=/dev/null`, no credential helper;
  - `protocol.allow=never` plus only the base URL's scheme;
  - the token becomes `http.extraHeader: Authorization: Basic x-access-token:…` through `GIT_CONFIG_COUNT` `[GIT-CONFIG]`, never in argv (world-readable in `/proc`), the URL, or `.git/config`;
  - stderr in errors is truncated and the token redacted.
- The base URL must be `https`; `http` is allowed only on loopback (tests). URLs with user info are refused.
- `Checkout.Path(rel)` resolves through symlinks and returns `ErrEscapes` outside the checkout (ADR-0004). `Cleanup(opID)`.

**Decisions**
- **Blobless single-branch clone:** it has the full commit history, so ancestry is exact, but downloads file contents for one commit only.
- **A SHA absent from the branch history is refused without fetching it.** Fork commits reachable through GitHub's network `[GH-FORKS]` therefore never enter the workspace.
- **Tests run against a real git smart-HTTP server** (`git http-backend` behind `net/http/cgi` on loopback), under the `integration` tag because they need the git binary. They need no network, so CI runs them too.

**Verification** (WSL2, git 2.43.0)
- `make lint`: exit 0. `make test` and `make test-integration`: all packages ok.
- `internal/source` integration (7 tests):
  - branch head checkout; a pinned ancestor with the **clone confirmed blobless** (missing blobs present);
  - **a feature-branch commit and an unknown SHA are refused as `ErrNotOnBranch`**, while the same commit works for its own branch; a missing branch returns `ErrBranchNotFound`;
  - separate workspaces per op, a retry wipes stale files, `Cleanup` touches only its op;
  - **the token reached the server as Basic `x-access-token`**, appears in no workspace file, and is not in error text;
  - `Path`: `evil -> /etc` and `../x` are refused, `link -> Dockerfile` is allowed;
  - bad inputs and base URLs (`ext::`, `file://`, plain `http` to a remote host, user info) fail before git is executed (checked with a nonexistent git binary).

**Next**
- P1.9: `internal/build`. It needs Docker and BuildKit, so it runs locally on WSL2.

### 2026-09-27: P1.7 CLI and deploy endpoint

- **Phase / task:** P1.7: CLI
- **Author:** Claude Code (desktop session)
- **Goal:** Operate Shipyard from a terminal (apps, environment, deploys) through the API only.

**Done** (commits `adceac8` Deploy endpoint, then CLI)
- API:
  - `POST /v1/apps/{app}/deployments` (`deploy` scope): an optional full commit `ref` (40 or 64 lowercase hex) and an `Idempotency-Key` (1–200 visible ASCII, stored as `api:<key>`, generated as `api:auto:…` when absent).
  - 202 for a new operation, 200 for a replay of the same request, 409 when the key was used for a different app or ref. The response lists superseded operations.
  - `GET /v1/operations/{id}`.
- `internal/client`: a typed client.
  - It refuses plain `http` to non-loopback hosts (the token would travel in clear) and supports `unix://` sockets.
  - It decodes problem+json into `*client.Error` with field errors, and path-escapes every segment.
- `cmd/shipyard`:
  - `login --url` reads the token from stdin, checks it with `whoami`, and only then saves `config.json` (mode 0600, written atomically). A config readable by others is refused.
  - `whoami`, `app create|list|show`, `ps`, `env set|unset|list`, `deploy`, `operation`.
  - Flags may come before or after positional arguments. Values are read from stdin with one trailing newline (LF or CRLF) dropped.
  - `SHIPYARD_URL`, `SHIPYARD_TOKEN`, and `SHIPYARD_CONFIG` override the config.

**Decisions**
- **`internal/client` is a new package** (ARCHITECTURE §8 updated). It keeps the CLI thin and testable.
- **`ps` equals `app list` for now.** It gains deployment status when deployments exist (P1.11).
- **An idempotent replay must be the same request**, not just the same key (payloads compared semantically), as with common payment APIs.

**Verification** (WSL2 as `hami`, plus native Windows)
- `make lint`: exit 0. `make test` and `make test-integration`: all packages ok.
- API integration `TestDeployEndpoint`: 202 → replay 200 → other ref 409 → other app 409; a keyless deploy supersedes; `GET` shows `cancelled` / `superseded by`; 422 for short or uppercase SHAs and bad keys; 400, 404, and 403 cases.
- Client unit tests: URL rules (7 accepted, 6 refused, including `http://10.0.0.5`); problem+json field errors; a non-JSON 502; `a/b` escaped as one segment.
- `TestCLIEndToEnd` against a real API and PostgreSQL:
  - a bad token login saves nothing; login, whoami;
  - two app creates (flags before and after the slug); 422 with field lines; ps, app show;
  - env set (secret and plain), list, unset, 404 on a missing key;
  - deploy, supersede, replay, short-ref error, operation;
  - **the token and the secret never appear in CLI output**.
- Native Windows: `go test ./cmd/shipyard ./internal/client ./internal/app` ok (local go1.27.1).
- **A Windows `shipyard.exe` against the API in WSL** (`http://127.0.0.1:18080`):
  - login, whoami, create, env set (CRLF input), env list, deploy, and ps all work;
  - a 422 lists both field errors; `http://example.com` is refused;
  - the secret appears 0 times in the API log.

**Problems / surprises**
- **Test harness, not product:**
  - Piping a script into `bash` lets WSL interop hand the rest of the script to the Windows exe as its stdin. Scripts now run from a file with `< /dev/null`.
  - `pkill -f 'shipyard-api serve'` matched its own shell; use `'[b]in/shipyard-api serve'`.
  - One run's `token create` returned nothing with stderr hidden. The rerun with stderr shown succeeded, so the cause is unknown.

**Next**
- P1.8: `internal/source`.

### 2026-09-27: P1.6 apps and env API

- **Phase / task:** P1.6: App CRUD API (plus the env endpoints and KEK wiring deferred from P1.4)
- **Author:** Claude Code (desktop session)
- **Goal:** Manage apps and their environment over the API, with validation before the database and uniform problem+json errors.

**Done** (commits `57137b1` App API, `92b2291` Sources dedupe, then Env API)
- `internal/app` (pure logic): `ValidateNew` and `ValidateUpdate` report every invalid field at once.
  - Branches follow git's ref-name rules `[GIT-REFNAME]` plus **no leading `-`** (option injection into git commands).
  - Paths are relative with no `..` component, backslash, or control character. The worker still resolves symlinks (ADR-0004).
  - Ports, durations, CPU, and memory are bounded.
- `internal/api`:
  - `/v1/apps` create (201 + Location), list, get, `PATCH` (slug and repo immutable), and `DELETE`. `{app}` is an ID or a slug.
  - Env: `GET /env` (keys and whether secret), `PUT` and `DELETE /env/{key}`. Values are secret by default and never echoed.
  - Strict JSON: `application/json` only (415), 1 MiB cap (413), unknown fields rejected, a single object (400).
  - `errors.go` is the single error → problem+json mapping. Validation errors return 422 with an `errors` list (an RFC 9457 extension member). Unexpected errors return a logged, generic 500.
- `store.DeleteIdleApp`: under the app lock, refuses (`ErrAppBusy` → 409) while an operation runs or a deployment is live.
- Config `SHIPYARD_KEK_DIR` (default `/etc/shipyard/kek`) and `SHIPYARD_KEK_ACTIVE`. `serve` loads the keyring before opening the DB or listener, and refuses to start without the active KEK or with a KEK file other users can read.
- `make dev-kek`: a dev-only KEK in the ignored `.dev/kek/`; `run-api` depends on it. `deploy/shipyard.env.example` documents KEK files.

**Decisions**
- **Validation lives in `internal/app`** (ARCHITECTURE §8: domain rules, no I/O). The database checks stay as the last line.
- **`migrate` and `token` do not need a KEK.** `SHIPYARD_KEK_ACTIVE` is required only by `serve` (and later by the worker).
- **Fix:** `GO-GCM` was defined twice in SOURCES.md, because P1.4 added a second row. The rows were merged, and a check for duplicate or undefined tags now passes.

**Verification** (WSL2 as `hami`)
- `make lint`: exit 0. `make test` and `make test-integration`: all packages ok.
- Unit: `CheckBranch` (7 good, 27 bad, including `--upload-pack=…`), `CheckRepoPath`, `ValidateNew` (11 field errors at once), `ValidateUpdate`, and the KEK config.
- API integration against real PostgreSQL:
  - CRUD by slug and ID; 409 on a duplicate slug; 422 with the exact field list;
  - 400, 413, and 415 cases; a read token gets 403; no ERROR logs for client errors;
  - delete refused while an op runs, then allowed; audit trail;
  - env set, list, unset; `Resolve` sees the sealed value; 404, 422, and 403 cases;
  - negative: the secret appears in no log line and no audit row.
- E2E with the real binary on `127.0.0.1:18080`:
  - `dev-kek` is idempotent, mode 600, 32 bytes;
  - `serve` without a KEK, or with a KEK file mode 644, is refused;
  - create 201; invalid input 422 listing `branch` and `build_context`; env set, list, patch;
  - **the secret appears 0 times in the API log and in a real `pg_dump`**; audit rows for every mutation.

**Next**
- P1.7: CLI. It needs `POST /v1/apps/{app}/deployments` (enqueue with `Idempotency-Key`) for `shipyard deploy`, so add that endpoint first.

### 2026-09-27: P1.5 operation queue

- **Phase / task:** P1.5: `internal/queue`
- **Author:** Claude Code (desktop session)
- **Goal:** The durable per-app operation queue of ADR-0002, safe under racing workers and crashes.

**Done** (commits `e728f97` Queue store, then Queue lease)
- `internal/store/operations.go`:
  - `EnqueueOperation`: `ON CONFLICT` idempotency. A key reused for another app or kind returns `ErrIdempotencyMismatch`. Latest-wins coalescing returns the IDs it superseded. An app row lock serializes admissions.
  - `ClaimOperation`: `FOR UPDATE SKIP LOCKED` that skips busy apps, `attempt < max_attempts`, and `attempt + 1`. It retries up to 3 times after losing a same-app race to the unique index.
  - `HeartbeatOperation`, `SetOperationPhase`, `CompleteOperation`, `FailOperation`: every write checks the lease owner, so a stale worker gets `ErrLeaseLost`. `FailOperation` can retry with a delay while attempts remain.
  - `RequeueExpired`: expired leases go back to the queue, or fail once the attempts are used up.
- `internal/store/events.go`: `AppendOperationEvent` with gapless per-operation `seq` (row lock, then the next seq read in a fresh statement). Messages are sanitized (NUL and invalid UTF-8 become U+FFFD) and truncated at 16384 characters. `OperationEvents(after, limit)` supports resume.
- `internal/queue`: `Queue.Next` polls every 2 s and logs DB errors without dying. `Queue.Hold` heartbeats every lease/3 and cancels the work (cause `ErrLeaseLost`) when the lease is lost or a whole lease passes without renewal.

**Decisions**
- **Admission creates only the operation**, per ARCHITECTURE §5 step 1. The worker creates the deployment at start (P1.11), so cancelled requests have no deployment rows. This replaces the P1.1 rationale for `deployments.operation_id`, which is still the link from a deployment to its operation.
- **`attempt` counts starts** and is incremented at claim. ADR-0002's "the reconciler increments attempt" is equivalent: an expired lease is simply claimed again.
- **Coalescing cancels queued operations of any kind** (deploy or rollback), since both change the serving release.

**Verification** (WSL2 as `hami`)
- `make lint`: exit 0. `make test` and `make test-integration`: all packages ok.
- Store integration (8 tests), including two race tests:
  - 12 workers against 4 apps, one with two queued ops: each op claimed once, one running per app.
  - 10 concurrent admissions for one app: exactly 1 queued and 9 cancelled.
  - Both passed **20 times in a row under `-race`**.
- Mutation checks:
  - without the app lock in admission, 4 ops stay queued instead of 1;
  - without conflict handling in claim, a worker returns a unique-violation error.
- `internal/queue` unit tests in `testing/synctest` bubbles (exact virtual time):
  - poll cadence and error logging, cancel;
  - heartbeats every 20 s for a 60 s lease;
  - stop on `ErrLeaseLost`;
  - give up at exactly 80 s after a last success at 20 s;
  - follow the parent context.
- Integration `TestLeaseHandover` (10 repeats, `-race`): while A holds, nothing expires and B claims nothing. When A's heartbeats fail, A stops itself, the op is requeued, B claims attempt 2, and A's `Complete` is refused.
- `TestEvents`: 20 concurrent appends produce gapless seqs 1–25; resume and limit work; NUL and invalid UTF-8 are stored sanitized.

**Problems / surprises**
- The first events test sent NUL bytes, and PostgreSQL rejected them (`22021`). Build output can contain NUL and invalid UTF-8, so the store now sanitizes messages.
- A lock plus `max(seq)` in a single statement does not serialize writers under READ COMMITTED, because the snapshot predates the lock wait. That is why events use two statements in a transaction.

**Next**
- P1.6: app CRUD API with validation and problem+json, plus `PUT/DELETE /v1/apps/{id}/env/{key}` and the keyring config (`SHIPYARD_KEK_DIR`, active KEK id).

### 2026-09-27: P1.4 secrets and environment revisions

- **Phase / task:** P1.4: `internal/secrets`
- **Author:** Claude Code (desktop session)
- **Goal:** Envelope-encrypt environment values and version them as immutable revisions (ADR-0005).

**Done** (commits `2a0ae5d` Envelope crypto, then Env revisions)
- `internal/secrets/envelope.go`: `Keyring` (`NewKeyring`, `LoadKeyring`), `Seal` and `Open`, `GenerateKey`, `NewValueID`.
  - A per-value DEK sealed with AES-256-GCM `NewGCMWithRandomNonce` `[GO-GCM]`, AAD `app_id|key|value_id`. The DEK is wrapped by the KEK with AAD `kek_id|value_id`.
  - Every open failure is `ErrDecrypt`, so the causes are indistinguishable.
  - KEK files are `<id>.key`, exactly 32 bytes, and must not be accessible to other users.
- `internal/secrets/env.go`: `Env.Set` (secret or plain), `Unset`, `Keys` (never values), and `Resolve` (worker only).
  - Each write locks the app row, then creates revision N+1 that references the unchanged entries.
  - Values are at most 64 KiB and contain no NUL.
- `internal/store/env.go`: `LockApp`, `InsertSecretValue`, `SecretValuesByID`, `CreateEnvRevision`, `LatestEnvRevision`, `EnvRevisionByID`.

**Decisions**
- **Value IDs are generated in Go** (UUID v4 from `crypto/rand`), because the ID is in the AAD and must exist before sealing.
- **`internal/secrets` uses `*store.Store` directly**, not an interface: it needs `InTx`, and its tests run against real PostgreSQL anyway.
- **Keyring wiring is deferred.** The config (`SHIPYARD_KEK_DIR`, the active id) arrives with the first consumers (P1.6 env endpoints, P1.10 container start). The roadmap says so.
- The KEK is below the 2³² message limit by many orders of magnitude, since each wrap is one message `[GO-GCM]`.

**Verification** (WSL2 as `hami`)
- `make lint`: exit 0. `make test` and `make test-integration`: all packages ok.
- Unit (8):
  - round trip including empty and 4 KiB values; no nonce or DEK reuse;
  - wrong AAD in 5 variants (other app, key, row; a DEK or ciphertext swapped in from another row);
  - wrong KEK and unknown KEK id; every single-byte tamper of the ciphertext and the wrapped DEK; truncation;
  - rotation (old values open, new ones seal with the new KEK); keyring validation; file mode refusal; UUID format.
- Integration (5):
  - revisions 1–4 with reuse by reference, an old revision still resolving the old secret, `Keys`, and `Unset`;
  - rejections (NUL, oversize, bad key, unknown app) leave no revision;
  - **Set with a keyring that cannot open the existing secret still works**, which proves Set never decrypts;
  - 8 concurrent Sets produce revisions 1–8 with all keys;
  - **dump of every table (`row_to_json`) contains neither the secret nor its hex**, while the plain value is present, which proves the check sees entry rows.
- Mutation check: without `LockApp`, `TestConcurrentSet` fails with `env_revisions_app_id_number_key` (3/3 runs).

**Next**
- P1.5: `internal/queue`.

### 2026-09-26: P1.3 token auth

- **Phase / task:** P1.3: Token auth
- **Author:** Claude Code (desktop session)
- **Goal:** Bootstrap the first admin token and protect every `/v1` route with scoped, expiring bearer tokens and an audit trail.

**Done** (commits `17c37fb` Token command, then Auth middleware)
- Tokens: `shp_` + 32 random bytes in unpadded base64url (47 characters). The display prefix is `shp_` plus 8 characters. Only the SHA-256 hash is stored `[GO-RAND]`.
- `shipyard-api token create|list|revoke`:
  - `create` makes the user if missing, prints the token alone on stdout and details on stderr, and audits `token.create` in the same transaction.
  - `--ttl` accepts 1h–366d (default 90d).
- Store:
  - Tokens: `CreateToken`, `ActiveTokenByHash` (revoked, expired, and unknown are all `ErrNotFound`), `TouchToken` (at most one write per minute), `ListTokens`, `RevokeToken` (idempotent).
  - Audit: `RecordAudit`, `AuditEvents`.
- `internal/api`:
  - `protect(scope, h)` wraps each route. 401 or 403 per `[RFC6750]`, same 401 for every bad token, well-formedness checked before any DB lookup, 503 without internal detail when the store is down.
  - An audit event after every authenticated mutation (actor `token:<prefix>`, action `r.Pattern`, target path, success/failure/denied, request ID), written with `context.WithoutCancel`.
- `GET /v1/whoami`.

**Decisions**
- **Scopes nest:** `read` ⊂ `deploy` ⊂ `admin`. `deploy` exists for CI tokens.
- **Anonymous failures are not audited**, only logged, so unauthenticated clients cannot grow the audit table.
- **The audit write is best-effort after the handler.** A failure is logged at ERROR and does not change the response. Writing audit in the mutation's own transaction is possible later, per use case.
- **`api_tokens.prefix` is `UNIQUE`** (edited in unreleased `0002`), so revoke-by-prefix is unambiguous.
- `internal/audit` (ARCHITECTURE §8) is not created yet: recording is one store call made by the middleware.

**Verification** (WSL2 as `hami`)
- `make lint`: exit 0. `make test` and `make test-integration`: all packages ok.
- Unit: 6 rejection cases (challenge header and whether the DB was queried), whoami with 3 scheme spellings, scope denial plus 3 audit results, audit surviving client cancel, audit and store failures. Negative: the plaintext is never in responses or logs.
- Integration: token lookups (active, no expiry, expired, unknown, revoked), idempotent revoke, touch throttle, unique prefix, hash, and non-empty scopes. `TestTokenCommand` bootstraps, lists, revokes, audits, and checks that the plaintext appears in no DB row.
- E2E with the real binary on `127.0.0.1:18080`:
  - no token → 401 `Bearer realm="shipyard"`; read token → 200 whoami; revoked → 401 `error="invalid_token"`;
  - token in the API log: 0 occurrences; audit rows for create and revoke; API exits 0 on SIGTERM.

**Problems / surprises**
- Port 8080 inside WSL was taken by the owner's `hamicloud-keycloak` container in Docker Desktop. All WSL2 distros share one network namespace. The e2e run used 18080, and DEVELOPMENT.md §5 has a row for it.
- WSL stops idle distros, which stops the dev PostgreSQL container. Run `make dev-up` again after a pause.

**Next**
- P1.4: `internal/secrets`. KEK file loading, per-value DEK, AES-256-GCM with AAD `app_id|key|value_id`, revision creation that reuses rows, plus store queries and the negative tests from the roadmap.

### 2026-09-26: P1.2 store core

- **Phase / task:** P1.2: `internal/store` on pgx
- **Author:** Claude Code (desktop session)
- **Goal:** One place for transactions and database error mapping, plus the first repositories.

**Done**
- `store.New`, `(*Store).InTx`. The same query methods run on the pool or in a transaction; a nested `InTx` joins the outer one.
- `errors.go`: SQLSTATE → `ErrNotFound`, `ErrConflict`, `ErrInvalid`, `ErrReference`, `ErrImmutable`, wrapped in `*ConstraintError{Table, Constraint, Column}`. PostgreSQL's message and detail are dropped because they quote values (`Key (slug)=(…)`), so the error is safe to log.
- `users.go`: `CreateUser`, `UserByName`. `apps.go`: `CreateApp`, `UpdateApp`, `AppByID`, `AppBySlug`, `ListApps`, `DeleteApp`. A nil `AppSettings` field keeps the DB default or the current value, so defaults live only in the migration.

**Changed files**
- `internal/store/{store,errors,users,apps}.go`, `errors_test.go`, `apps_integration_test.go`
- `migrations/0002_schema_v1.sql`: immutability SQLSTATE and owner FK (below). `docs/SOURCES.md`: `PG-RAISE`. `docs/ROADMAP.md`

**Decisions**
- **Scope:** P1.2 is the core plus users and apps. Each later task adds the queries it consumes (CLAUDE.md §5: interfaces are declared by the consumer). The roadmap item says so.
- **Durations** are exchanged as microseconds (`extract(epoch …)` / `$n * interval '1 microsecond'`), independent of the driver's interval mapping.

**Verification** (WSL2 as `hami`, PostgreSQL 18)
- `make lint`: exit 0. `make test`: ok, including `TestMapError` and `TestConstraintErrorOmitsValues` (negative test: the rejected value never appears in `Error()`).
- `make dev-reset && make dev-up && make migrate`: `applied=2`, then `applied=0`.
- `go test -race -tags integration ./internal/store/`: all pass (users, app defaults, round trip, 9 rejection cases, update/list/delete, `InTx` rollback, nested join and commit, owner delete refused). `make test-integration`: all ok.

**Problems / surprises**
- **A test caught a real bug.** `ON DELETE RESTRICT` raises `23001 restrict_violation`, the same code the immutability trigger used, so "owner still has apps" would have surfaced as "row is immutable". Fixed in `0002`:
  - the trigger now raises Shipyard's own `SY001` `[PG-RAISE]`;
  - `apps.owner_id` uses the default `NO ACTION` (`23503`).
  `0002` was edited in place because it is unreleased and only on this branch. Any dev database that applied the old `0002` needs `make dev-reset`.

**Next**
- P1.3: token auth. Add `api_tokens` and `audit_events` queries to `internal/store` with integration tests.

### 2026-09-26: P1.1 schema v1

- **Phase / task:** P1.1: Schema v1
- **Author:** Claude Code (desktop session)
- **Goal:** Turn the ARCHITECTURE §4 data model into migration `0002` with database-enforced invariants.

**Done**
- `migrations/0002_schema_v1.sql`: 12 tables (the roadmap's 11 plus `env_revision_entries`), 2 trigger functions, and the indexes the queue needs.
- Enforced in the database:
  - `UNIQUE(idempotency_key)`; one running operation per app; one active deployment per app; unique lowercase `hostname`.
  - A running operation needs a lease; `finished_at` matches terminal status; `failed` needs a reason; serving states need `image_id` and `container_id`.
  - Composite `(app_id, id)` foreign keys, so no cross-app operation, env revision, rollback source, route target, or secret. An entry's secret must also carry the entry's key (it is in the AAD, ADR-0005).
  - Immutability triggers on secret values, revisions, and entries; append-only operation events; audit events reject UPDATE and DELETE.
  - Cheap path checks: `dockerfile_path` and `build_context` cannot be absolute or contain a `..` segment (the worker still resolves symlinks, ADR-0004).
- `internal/store/schema_integration_test.go`: 33 subtests asserting SQLSTATE codes.

**Changed files**
- `migrations/0002_schema_v1.sql`, `internal/store/schema_integration_test.go`: the slice
- `docs/ARCHITECTURE.md` §4: ID and timestamp conventions, new columns; `docs/SOURCES.md`: `PG-UUID`, `DK-RESOURCES`; `CHANGELOG.md`; `docs/ROADMAP.md`

**Decisions**
- **IDs are `uuid` via `gen_random_uuid()`**, not `uuidv7()`: `uuidv7()` needs PostgreSQL 18, while ADR-0002 still accepts 17. They are not enumerable through the API.
- **Immutable and append-only tables have no `updated_at`**, which is a deviation from "every table has `updated_at`". ARCHITECTURE §4 is updated.
- **Added columns** not listed in §4: `deployments.operation_id` (a deployment row exists from admission, so coalescing can mark it `cancelled`), `deployments.source_deployment_id` (rollback target), and `api_tokens.name`.
- **`slug` is limited to 40 characters** (one DNS label) so that container and network names stay short.
- Enumerations are `text` plus `CHECK`, so adding a value takes a one-line migration.

**Verification** (WSL2, as `hami`, PostgreSQL 18 in `make dev-up`)
- `make lint`: exit 0 (gofmt, vet, and staticcheck, including integration files). `make test`: ok.
- `make migrate`: applied versions 1 and 2, then `applied=0`.
- `go test -tags integration -run TestSchema ./internal/store/`: 33/33 subtests PASS. `make test-integration`: all packages ok.
- Mutation check: after removing the running-op index, the active-deployment index, and the audit DELETE guard, exactly those 3 subtests failed.

**Next**
- P1.2: `internal/store` repositories on pgx (apps, operations, deployments, env revisions, tokens, audit) with integration tests.

### 2026-09-26: Release v0.1.0

- **Phase / task:** P0 close: merge and first release
- **Author:** Claude Code (desktop session), on the owner's explicit request
- **Goal:** Merge Phase 0 to `main` and cut `v0.1.0` per docs/RELEASING.md.

**Done**
- Fast-forward merge of `claude/shipyard-architecture-proposal-j8w56b` into `main` (`73fa9e4`).
- `CHANGELOG.md`: `[Unreleased]` → `[0.1.0] - 2026-09-26` (`3000c74 Release v0.1.0`), annotated tag `v0.1.0` pushed.
- WSL user `hami` added to the `docker` group (already in `sudo`); repo cloned to `/home/hami/shipyard`.
- README: release pointer and image usage (no entrypoint; name the binary).

**Verification**
- Rehearsal in WSL as `hami`: `make release-check` ok; `make release-snapshot` ok (8 archives, checksums, amd64/arm64 images); binaries report `go1.26.8`.
- CI on `main`: `73fa9e4` and `3000c74` both green. [Release run](https://github.com/hami9/Shipyard/actions/runs/36252517257): success.
- Release page: not a draft or pre-release, 8 archives plus `checksums.txt`. `sha256sum -c` OK for the Linux server and Windows CLI archives; `gh attestation verify` exit 0; downloaded `shipyard.exe version` → `v0.1.0 (commit 3000c741483a, go1.26.8)`.
- GHCR: anonymous `docker manifest inspect` works (amd64, arm64, plus attestation manifests); `shipyard`, `shipyard-api`, and `shipyard-worker` all report `v0.1.0` from the image; `:latest` resolves.

**Problems / surprises**
- The GHCR package was already publicly pullable after the first push, so the manual "make it public" step was not needed this time. Package settings could not be read here (`gh` token lacks `read:packages`).

**Next**
- P1.1: `git switch -c schema-v1 main`, then write `migrations/0002_schema_v1.sql` plus store integration tests.

### 2026-09-26: Owner local run (WSL2), repo settings

- **Phase / task:** P0 exit criteria (local verification), owner blockers
- **Author:** Claude Code (desktop session on the owner's Windows 10 machine)
- **Goal:** Clear the owner-side Phase 0 blockers and run DEVELOPMENT.md §2–§3 locally.

**Done**
- GitHub, via `gh`: set the About description and 10 topics; enabled private vulnerability reporting (`{"enabled":true}`).
- WSL2: installed `Ubuntu-24.04` (systemd on), Docker Engine 29.8.1, Compose v5.5.1, and go1.26.8 (checksum OK), following §2.3–§2.4. Docker Desktop stays installed; its WSL integration is not enabled for Ubuntu.
- Fixed a dev-env race: on a fresh volume, `make dev-up && make migrate` failed with `57P03 the database system is starting up`. The healthcheck probed the Unix socket, which the image's init-only temp server already serves `[PG-IMAGE-INIT]`. It now probes `127.0.0.1`.
- Added WSL troubleshooting rows (VPN DNS, apt IPv6, Docker Desktop CLI on PATH) and tags `PG-IMAGE-INIT`, `MS-WSL-CONF`.

**Changed files**
- `deploy/dev/compose.yaml`: TCP healthcheck
- `docs/DEVELOPMENT.md`, `docs/SOURCES.md`, `docs/ROADMAP.md`: troubleshooting, tags, exit criteria

**Decisions**
- none (dev-only config fix)

**Verification** (inside Ubuntu-24.04 on WSL2, as root, commit `b254f91` plus the fix)
- §2.5: `docker info` → `Ubuntu 24.04.5 LTS`; probe of container IP `172.17.0.2` → `HTTP 200`.
- `make lint`: exit 0. `make test`: 8 packages ok under `-race`.
- `make build && ./bin/shipyard-api version` → `shipyard-api b254f91 (commit b254f91ae4c5, go1.26.8)`.
- Before the fix: first `make migrate` after `dev-up` → 57P03; container log showed the Unix-socket-only temp server.
- After the fix: `make dev-reset`, then `dev-up && migrate && test-integration` **3/3 PASS**; second `migrate` → `applied=0`.
- `curl /healthz` → `200 {"status":"ok"}` with `X-Request-Id`; `curl /readyz` → `200 {"status":"ready"}`.
- API binary exits 0 on SIGINT and on SIGTERM, logging `api shutting down`.

**Problems / surprises**
- With the Windscribe VPN connected, WSL's NAT DNS proxy did not resolve anything, although IPs were reachable. Fixed per distro with `generateResolvConf=false` plus public resolvers `[MS-WSL-CONF]`. `dnsTunneling` is not available on Windows 10.
- apt tried IPv6 and got `Ign:` on every package; forced IPv4 (`99force-ipv4`, `gai.conf`).
- No regular Linux user exists in the distro yet; everything ran as root. The owner creates one (see Next).

**Next**
- Owner: create the WSL user (`wsl -d Ubuntu-24.04`, then `adduser <name>`, `usermod -aG sudo,docker <name>`, and set `[user] default=<name>` in `/etc/wsl.conf`).
- Owner: merge this branch to `main`, then cut `v0.1.0` per docs/RELEASING.md and make the GHCR package public.
- Agent: P1.1, schema v1 migration `0002_schema_v1.sql` plus store tests.

### 2026-09-25: Owner platform, WSL2 guide

- **Phase / task:** P0 (exit criterion: local verification)
- **Author:** Claude Code (cloud session)
- **Goal:** Tailor local testing to the owner's machine: **Windows with WSL2**, and no Docker installed yet.

**Done**
- `docs/DEVELOPMENT.md` §2 is now a WSL2 guide, sourced step by step:
  - WSL version check, systemd check,
  - Docker Engine from Docker's apt repository (not Docker Desktop),
  - Go 1.26.8 from the official tarball with its SHA-256,
  - sanity checks, including a host-to-container-IP probe.
- 4 new source tags: `DK-INSTALL-UBUNTU`, `MS-WSL-SYSTEMD`, `MS-WSL-FS`, `GO-INSTALL`.
- CLAUDE.md: a rule to create files with the editor tools, never shell heredocs (see below).

**Decisions**
- Docker Engine runs natively inside WSL2 Ubuntu, and Docker Desktop is excluded. Its bridge network is unreachable from the host `[DK-DESKTOP-NET]`, which would break Phase 1 health probes.

**Verification**
- CI [run #4](https://github.com/hami9/Shipyard/actions/runs/36200413332) on `d4c3a5a`: **success**, including the new GoReleaser config check.
- The probe from §2.5 was run on native Docker Engine in the cloud container: `HTTP 200` from the container's bridge IP (172.17.0.2).
- The go1.26.8 tarball checksum in the guide matches `go.dev/dl/?mode=json`, and `sha256sum -c` passed.
- Not run: the guide itself on WSL2 (the owner will).

**Problems / surprises**
- **Incident (cloud container only):** a shell heredoc used to insert the Markdown section contained its own `EOF` line. That ended the outer heredoc early, and bash executed the rest of the section as commands.
  - Effects: Docker packages were upgraded mid-run, `/usr/local/go` was replaced with go1.26.8, and a PATH line was added to `~/.profile`.
  - There was no effect on the repository (`git status` clean), GitHub, or the owner's machine.
  - Cleaned up: the profile line was reverted, the mismatched dockerd was stopped, and the dev PostgreSQL was removed.
  - Prevention: the CLAUDE.md rule above.

**Next**
- Owner: DEVELOPMENT.md §2 (setup) and §3 (verification). Send back §2.5, `make lint`, `make test`, `make test-integration`, and the two curl outputs.

### 2026-09-25: Public-repo readiness (P0.8)

- **Phase / task:** P0.8 (added at the owner's request: releases, packages, description, license)
- **Author:** Claude Code (cloud session)
- **Goal:** Make the public repository complete: license, release artifacts and packages, and community files.

**Done**
- Owner decisions: **Apache-2.0**, and **binaries plus a GHCR image** per release.
- `LICENSE` is the canonical Apache-2.0 text (SHA-256 `cfc7749b…d30`). Added `NOTICE` ("The Shipyard Authors").
- `.goreleaser.yaml` (GoReleaser v2.18.2):
  - CLI for Linux, macOS, and Windows on amd64 and arm64.
  - `shipyard-server` for Linux amd64 and arm64, bundling `deploy/`.
  - `checksums.txt`.
  - `dockers_v2` image `ghcr.io/hami9/shipyard`, on distroless `static-debian13:nonroot` pinned by digest, with OCI labels (source, license).
- `.github/workflows/release.yml`: runs on a `v*` tag.
  - Lint and test, then release notes from CHANGELOG.
  - QEMU and buildx, a GHCR login, and GoReleaser.
  - `actions/attest@v4` over `checksums.txt`.
- CI now also validates the GoReleaser config.
- `scripts/release-notes.sh`: fails if CHANGELOG has no section for the tag.
- `scripts/check-go-version.sh`: fails the build unless Go matches go.mod's toolchain.
- `SECURITY.md` (private reporting, scope aligned with the trust model), `CONTRIBUTING.md`, `CHANGELOG.md` (Keep a Changelog), and `docs/RELEASING.md` (steps, version plan `v0.1.0`…`v1.0.0`).
- README: badges, Install, Security, and License sections.
- CLAUDE.md: a changelog rule, and agents never tag or release unless the owner asks.

**Changed files** (commits)
- `7230bbb` License · `3dfcede` Release pipeline · `343d549` Community files

**Decisions**
- Release notes come from CHANGELOG.md rather than commit messages. The 1–2 word commit subjects are too terse for users.
- The image has no ENTRYPOINT (CMD `shipyard help`). The supported production install stays systemd. The image is for the CLI and for evaluation.
- GoReleaser is pinned to exactly v2.18.2 (in the workflow and the Makefile) for reproducible releases.

**Verification**
- `make release-check`: 1 configuration file validated, with no deprecation warnings.
- `make release-snapshot` (local, nothing published): 8 archives plus checksums. amd64 and arm64 images were built.
  - `sha256sum -c` OK.
  - The server archive contains the binaries and `deploy/`.
  - The image runs as `nonroot:nonroot` with source and license labels.
  - `shipyard-api version` inside the image works.
- **Bug found and fixed:** the first snapshot built binaries with **go1.27.1**. GoReleaser v2.18.2 requires Go 1.27.1, and `go run` passed its toolchain to the builds. The fix installs GoReleaser as a binary and adds the toolchain guard hook. After the fix, `go version` on the binaries shows go1.26.8. The guard was tested negatively: with `GOTOOLCHAIN=go1.27.1` the release fails with "building with go1.27.1 but go.mod pins go1.26.8".
- `scripts/release-notes.sh`: fails on a missing section (exit 1) and extracts the right section from a sample changelog.
- `make lint test`: clean, 8 packages ok. All YAML files parse.
- Not run: the real tag-triggered release (no tag was pushed, per the owner's release process).

**Problems / surprises**
- GitHub gives new GHCR packages **private** visibility, so the owner must make the package public after the first release `[GHCR]`.
- The repository About text (description, topics) cannot be set from this session. The owner sets it in the GitHub UI.

**Next**
- Owner: local run (docs/DEVELOPMENT.md §3) and OS; About text; enable private vulnerability reporting.
- Then close Phase 0, merge to `main`, cut `v0.1.0` (docs/RELEASING.md), and start P1.1.

### 2026-09-25: Phase 0 bootstrap (P0.1–P0.7)

- **Phase / task:** P0.1–P0.7
- **Author:** Claude Code (cloud session)
- **Goal:** Record the owner's approvals and build the fully wired, empty-but-working project skeleton.

**Done**
- The owner accepted ADR-0001 to ADR-0007. Docker- and Caddy-dependent tests now run on the owner's local machine (CLAUDE.md §6).
- Go module `github.com/hami9/shipyard` with three binaries:
  - `shipyard` (version),
  - `shipyard-api` (`serve`, `migrate`, `version`),
  - `shipyard-worker` (`run`, `version`).
  All shut down gracefully on SIGTERM.
- `internal/config`: environment-only, validated, and reporting all errors at once. The API refuses a non-loopback listen address unless explicitly overridden.
- `internal/logging`: slog JSON or text, with `request_id`, `operation_id`, `app`, and `deployment_id` taken from the context.
- `internal/api`:
  - `GET /healthz` (liveness) and `GET /readyz` (DB ping, 503 problem+json).
  - Request IDs, with client IDs accepted only from a safe charset.
  - An access log that omits query strings.
  - Unix socket listen (mode 0660, stale-socket safe).
- `internal/store`: `Open` (ping; errors redact the password), plus an in-house migration runner (`embed` + pgx).
  - One transaction per run, serialized by `pg_advisory_xact_lock`.
  - Refuses renamed or unknown applied migrations.
  - `storetest.NewDatabase` gives each integration test a throwaway database.
- `migrations/0001_baseline.sql`.
- Makefile (`help`, `build`, `test`, `lint`, `fmt`, `test-integration`, `dev-up/down/reset`, `migrate`, `run-api`, `run-worker`, `clean`) and CI (`.github/workflows/ci.yml`).
- `deploy/`: hardened systemd units, `daemon.json` (`local` log driver, `live-restore`), Caddy bootstrap with the admin socket at `|0220`, `shipyard.env.example`, and the dev compose file (PostgreSQL 18 on 127.0.0.1:54320).
- `docs/DEVELOPMENT.md` (local testing guide) and 7 new source tags.

**Changed files** (commits)
- `ea9afba` Accept ADRs: ADR statuses, CLAUDE.md testing policy
- `2cf3a7c` Config logging · `2df3f84` Migrations · `6730d1c` API server · `d8605b2` Binaries
- `2b40117` Tooling · `f770e7c` Deploy skeleton · `93108e9` Dev docs

**Decisions**
- **Config is environment variables only**, supplied by systemd `EnvironmentFile=`. This avoids a parser dependency (TOML was implied before).
- **In-house migration runner** instead of goose or golang-migrate. It is about 150 lines on pgx, with no extra dependency, and is fully tested.
- **Toolchain pinned to `go1.26.8`.** staticcheck 2026.2.1 fails on Go 1.27.1's standard library (`method must have no type parameters`) `[STATICCHECK]`.
- **Dropped a public `GET /version` endpoint**, to avoid exposing version and commit without authentication.
- Only one new dependency: `github.com/jackc/pgx/v5` v5.11.0, which was already in the approved stack.

**Verification** (all run in the cloud container)
- `make lint`: gofmt clean, `go vet` (plus `-tags integration`) clean, staticcheck (plus `-tags integration`) clean.
- `make test`: 8 packages ok under `-race`.
- `make dev-up`: postgres:18 healthy (`PostgreSQL 18.6`).
- `make migrate` twice: `applied=1`, then `applied=0`.
- `make test-integration`: all ok. The store integration tests passed: applies once, rollback on failure, refuses a newer schema, refuses a rename, concurrency × 5, embedded migrations. They also passed on PostgreSQL 16.13.
- Smoke tests:
  - `/healthz` and `/readyz` return 200 over TCP and over a Unix socket (mode 660).
  - A `0.0.0.0` listen address is refused with exit 1.
  - The API and worker exit 0 on SIGTERM.
- `systemd-analyze verify`: no syntax errors (it only reported that the binaries are not installed).
- GitHub Actions: [run #1](https://github.com/hami9/Shipyard/actions/runs/36199600490) (`93108e9`) and [run #2](https://github.com/hami9/Shipyard/actions/runs/36199667568) (`c70dc39`) both **success**, covering both jobs (lint/unit/build and integration on PostgreSQL 18).
- Not run yet: the owner's local run.

**Problems / surprises**
- Docker in the cloud container defaults to `json-file` logging, which confirms that the `daemon.json` change is needed `[DK-LOG]`.
- Docker Desktop cannot reach container bridge IPs from the host `[DK-DESKTOP-NET]`. Added to the risk register and DEVELOPMENT.md.

**Next**
- Owner: follow `docs/DEVELOPMENT.md` §3 and send back the output and their OS.
- Agent: once the owner's run passes, close Phase 0 and start P1.1 (schema v1 migration `0002_schema_v1.sql` plus store tests).

### 2026-09-25: Architecture review, agent instructions, roadmap

- **Phase / task:** Pre-Phase 0: project definition
- **Author:** Claude Code (cloud session)
- **Goal:** Correct the proposed architecture against primary sources, create the agent system prompt and work log, and phase the roadmap.

**Done**
- Archived the original proposal as `docs/archive/ARCHITECTURE-v1.md`.
- Verified v1 claims against GitHub, Docker, Caddy, Let's Encrypt, PostgreSQL, Go, OWASP, WHATWG, MDN, and RFC 9457 docs (38 sources, tagged).
- Wrote `docs/ARCHITECTURE.md` v2 with 19 corrections (R1–R19). High-severity items:
  - Caddy admin API on a Unix socket (R2)
  - `local` log driver, since json-file never rotates (R3)
  - Commit ancestry check against fork-network commits (R4)
  - Envelope encryption moved into Phase 1 (R9)
  - Docker-published ports bypass ufw (R10)
- Wrote proposed ADRs 0001–0007 that answer v1's open questions.
- Wrote `docs/ROADMAP.md` with Phases 0–7, IDs, exit criteria, and a dependency graph.
- Created `CLAUDE.md` (agent system prompt), `AGENTS.md`, this log, `README.md`, `.gitignore`, and `.editorconfig`.
- Adopted the owner's git conventions: commit subjects of 1–2 words, branch names of 1–3 words.

**Changed files**
- `docs/ARCHITECTURE.md`, `docs/architecture-review.md`, `docs/SOURCES.md`, `docs/archive/ARCHITECTURE-v1.md`: `242e169` Architecture v2
- `CLAUDE.md`, `AGENTS.md`: `2622d14` Agent prompt
- `docs/adr/0000`–`0007`: `63c8f8d` ADRs
- `docs/ROADMAP.md`: `24d6ec5` Roadmap
- `README.md`, `.gitignore`, `.editorconfig`: `af133ba` Scaffold
- `docs/WORKLOG.md`: Worklog commit

**Decisions**
- All seven ADRs are `Proposed`, pending the owner's review.
- Notable reversals from v1: secrets move from milestone 5 to Phase 1, "image digest" becomes the Engine image ID plus build metadata, and advisory locks become lease rows plus a partial unique index.

**Verification**
- Documentation only; no code exists yet. Source facts were fetched from primary docs on 2026-09-25 (see `docs/SOURCES.md`).
- A link and tag check script found 0 broken relative links, 38 defined source tags, 0 undefined, and 0 unused.
- Version facts at verification time: Go 1.27.0 (2026-08-19), PostgreSQL 18.6, Docker Engine 29.8.1.

**Problems / surprises**
- `docker/docker` Go module deprecated in Engine 29 → use `github.com/moby/moby/client` `[DK-29]`.
- GitHub compare API docs say `BASE...HEAD` must be branch names, so the ancestry check uses `git merge-base --is-ancestor` rather than relying on the API.
- The session's assigned branch `claude/shipyard-architecture-proposal-j8w56b` exceeds the new 1–3 word branch rule. It was kept because the harness requires it, and future branches follow the rule.

**Next**
- Owner reviews ADR-0001 to ADR-0007 (accept or amend).
- Start P0.1: `go mod init`, `cmd/shipyard{,-api,-worker}` skeletons, Makefile, CI workflow.
