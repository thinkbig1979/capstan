# Capstan E2E Testing

End-to-end browser tests for Capstan, written in [Playwright](https://playwright.dev/).

```
testing/
├── README.md
├── tests/playwright/
│   ├── helpers/
│   │   └── network-settle.ts        # Settle guard used instead of networkidle waits
│   ├── auth-session.spec.ts         # Setup, login, session revocation
│   ├── backup-flow.spec.ts          # Backup configure -> run -> restore
│   ├── dashboard-backups.spec.ts    # Dashboard Backups tab + Auto backup toggle
│   ├── network-settle-guard.spec.ts # Regression controls for helpers/network-settle.ts
│   └── terminal-flow.spec.ts        # Start stack -> open shell -> run a command
└── reports/                         # Playwright reporter output (generated, gitignored)
```

## Running locally

Specs are driven by the repo-root `playwright.config.ts`, which sets
`testDir: './testing/tests/playwright'` and discovers every `*.spec.ts` under it.

```bash
pnpm install --frozen-lockfile
npx playwright install --with-deps chromium

npx playwright test                    # all specs
npx playwright test backup-flow        # one spec
npx playwright test --reporter=line    # compact output
```

Four of the five specs need a running backend and frontend;
`network-settle-guard.spec.ts` needs neither. The environment variables the
browser specs read (base URL, API URL, test credentials, stack name, restic
repo path) are documented in the header comment of `playwright.config.ts`.

The specs split across *two* backends, which cannot be the same one:

| Spec | Backend requirement |
|------|---------------------|
| `auth-session.spec.ts` | `AUTH_DISABLED=false` and a virgin `DATA_DIR` — its first test performs the one and only `POST /auth/setup` and asserts `needsSetup` was true beforehand |
| `backup-flow.spec.ts` | `AUTH_DISABLED=true`, restic 0.18.0+ on `PATH`, a Docker daemon (the backup path stops the stack before snapshotting), and a pre-existing stack named `test-app`. 0.18.0 is the floor the backup exit codes were measured against (`backend/internal/services/backup.go`); CI and the shipped image install 0.19.1 (`.github/workflows/e2e-backup.yml`, `docker/Dockerfile`), which is what they pin, not a minimum this suite requires |
| `dashboard-backups.spec.ts` | The same backend as `backup-flow.spec.ts`: `AUTH_DISABLED=true`, restic 0.18.0+ on `PATH`, a Docker daemon, and a pre-existing `test-app` stack. Runs in the backup-flow CI job. Documented with `--retries=0`, because `playwright.config.ts` retries once under CI and Playwright reports a fail-then-pass as FLAKY, not as passed |
| `network-settle-guard.spec.ts` | None — no backend, no frontend, no browser. Its eight tests drive a stub page object to assert the settle guard in `helpers/network-settle.ts` still refuses the orders that defeated an earlier revision of it. Runs in the backup-flow CI job because that job selects everything except `auth-session`, not because it needs that job's backend |
| `terminal-flow.spec.ts` | `AUTH_DISABLED=true` and a Docker daemon — it starts the `test-app` stack itself and opens a real shell into its container. Runs in the backup-flow CI job (same backend) |

### Running `backup-flow.spec.ts` with auth on

CI runs it with `AUTH_DISABLED=true`. It also works against a backend with auth on:
leave `AUTH_DISABLED` unset (or `false`) for both the backend and the spec, and set
`CAPSTAN_TEST_USER` / `CAPSTAN_TEST_PASSWORD` to an account that already exists.
The login route allows 5 requests a minute per (IP, account), and the API login and
the UI login form share that bucket. The spec therefore logs in once, in `BACKUP-PW-001`, and plants that session cookie in each test's browser
(`loginIfNeeded`). It falls back to the login form only if the cookie is not
accepted, for example when `CAPSTAN_API_URL` and `CAPSTAN_BASE_URL` are on different
hosts. Run the whole file, not one test at a time: tests after `001` read the
session it saved.

`dashboard-backups.spec.ts` still logs in through the form in each of its tests, so
an auth-on local run of it can hit the same limit.

## Type-checking the Playwright config and specs

`tsconfig.e2e.json` at the repo root covers `playwright.config.ts` and
`testing/tests/playwright/`. Nothing else does: `frontend`'s `tsc -b` only reads
`frontend/`. CI runs it in the `backup-flow` job, after both `pnpm install` steps:

```bash
./frontend/node_modules/.bin/tsc -p tsconfig.e2e.json
```

The root installs only Playwright, so node's types come from `frontend/node_modules/@types`
(`typeRoots` in the tsconfig). Run `pnpm install --frozen-lockfile` in `frontend/` first.

## Browser checks against the production bundle

To check the built frontend in a browser (instead of the `vite` dev server), build
it and serve it with `vite preview` **from `frontend/`**, with a backend on `:5001`:

```bash
cd frontend
./node_modules/.bin/vite build
./node_modules/.bin/vite preview --port 3002 --strictPort
```

Run it from `frontend/` because that is where `vite.config.ts` lives. From any
other directory vite loads no config at all, `/api` stops proxying, and the SPA
fallback answers it with `index.html` and a 200, not an error.

`vite preview` inherits `server.proxy` from the config: vite resolves the preview
proxy as `preview?.proxy ?? server.proxy` (checked in vite 8.3.1; first observed
on 8.2.1). So `frontend/vite.config.ts` has no `preview` block and must not get
one: it would duplicate the `:5001` target in two places. A missing proxy under
`vite preview` means the wrong working directory, not missing preview support.

Both outcomes on one URL, with a backend on `:5001` (observed 2026-10-08):

```bash
# From frontend/: backend JSON
$ curl -si http://localhost:3002/api/v1/version | grep -iE '^HTTP|^content-type'; curl -s http://localhost:3002/api/v1/version
HTTP/1.1 200 OK
content-type: application/json; charset=utf-8
{"version":"dev","commit":"unknown","buildDate":"unknown"}

# From the repo root (./frontend/node_modules/.bin/vite preview --port 3002 --strictPort --outDir frontend/dist): index.html
$ curl -si http://localhost:3002/api/v1/version | grep -iE '^HTTP|^content-type'; curl -s http://localhost:3002/api/v1/version | head -2
HTTP/1.1 200 OK
Content-Type: text/html
<!doctype html>
<html lang="en">
```

The status is 200 in both cases, so check `Content-Type`. Any `/api/v1/...` route
that returns JSON works for this check; `/api/v1/version` needs no login.

## Running in CI

`.github/workflows/e2e-backup.yml` runs the suite on every pull request, on push
to `main`, and nightly at 03:17 UTC. It uses two jobs with two separate backends
and routes tests by path, not by tag:

| Job | Command | Selects |
|-----|---------|---------|
| `backup-flow` (`AUTH_DISABLED=true`) | `npx playwright test --grep-invert "auth-session"` | Every spec except `auth-session.spec.ts` |
| `auth-session` (`AUTH_DISABLED=false`) | `npx playwright test --grep "auth-session"` | `auth-session.spec.ts` only |

**Neither command names a spec file, so both auto-discover.** `--grep-invert`
and `--grep` match against the full test title, which includes the file path;
Playwright itself enumerates `testDir`. A new spec is therefore picked up by the
`backup-flow` job with no workflow edit at all. That is convenient and it is
also exactly why this document rotted: `dashboard-backups.spec.ts` and
`network-settle-guard.spec.ts` both entered CI without a single diff line that
would have prompted anyone to update the tables above. Nothing enforces the
match between this file and `testing/tests/playwright/` — only the habit of
editing them together.

That workflow's header comment carries the full rationale for the split, the
runner choices, and the reporter flags; read it before changing how the suite is
invoked.

## Reports

`playwright.config.ts` configures `list`, `html`, and `json` reporters writing
to `testing/reports/`. Do not pass `--reporter` on the command line in CI: it
replaces the whole configured array, including the custom output paths, and the
report-upload steps then find nothing.

## Adding a spec

Drop a `*.spec.ts` file into `testing/tests/playwright/` and it is picked up
automatically — no workflow edit needed. Check which of the two CI jobs it
belongs to first: a new spec runs in the `backup-flow` job by default, since
that job selects everything except `auth-session`. A spec needing real
authentication must get its own job with its own isolated backend.

Then add it to the file tree and the backend-requirement table above. Nothing
checks that for you.
