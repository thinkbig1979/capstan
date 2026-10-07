# Safe Defaults: use the helper, not the raw call

<!-- PROJECT-OWNED (agent-os-y36y, 2026-10-06). Source and evidence:
     Supporting-Docs/evaluations/capstan-review-learnings.md (main tree only,
     gitignored), sections 2, 3 and 5. Rule numbers match its section 3. -->

An external review of Capstan (2026-10) found that most defects shared one
shape: the safe helper already existed, but each new call site had to remember
to use it, and some didn't. Fixes closed the instance and left the class open.
So the rules below name the helper to use. Check every diff against them, and
treat a raw call where a helper exists as a defect, not a style choice.

When a fix routes code through one of these helpers, the bead's close names the
guard that keeps every site on it, or the bead filed for one (the Guard field in
root CLAUDE.md's close-time block). Don't convert the other sites in the same
fix: file them.

## Bounded resources

1. **Child processes**: build every non-interactive one with
   `commandWithDeadline` (`backend/internal/services/exec_env.go`) and pass its
   error through `timeoutError`, so a timeout reads as one. The context needs a
   deadline from the operation, not only a cancel. The interactive `docker exec`
   in `handlers/terminal.go` is the one documented exception.
2. **WebSockets**: every socket comes from `upgradeConnection`
   (`backend/internal/handlers/ws.go`), which sets the read limit. A frame kind
   that needs bigger frames sets `wsRegistration.readLimit`; it never bypasses
   the helper. (CI-guarded: `scripts/check-ws-registration.sh`.)
3. **Boot-time goroutines** exit only when their context ends. A `return` on a
   transient error inside such a loop is a defect: reconnect with backoff, as
   `ListenEvents` in `backend/internal/services/monitor.go` does. A channel send
   in a producer selects on `ctx.Done()`.

## Guards that must be taken

4. **Stack operations**: anything that runs compose, writes under a stack
   directory, or starts/stops/removes a stack's container takes that stack's
   lock via `acquireStackLock` (`backend/internal/handlers/operation_lock.go`)
   or `OperationLock.Acquire(stackID, kind)`, and releases with the returned
   token.
5. **Optional guard dependencies** (nil means off) ship with a wiring test that
   proves `main.go` installs them, in the shape of
   `backend/cmd/server/oplock_wiring_test.go` (it uses `go:embed`, so `-overlay`
   mutants are visible to it; a test that reads `main.go` from disk is not).
6. **Path containment** uses `IsContained` (`backend/internal/pathutil/pathutil.go`)
   or `MatchStacksRoot` (`backend/internal/config/config.go`), never
   `strings.HasPrefix` or a lexical `filepath.Rel` check on paths.
   (CI-guarded: `scripts/check-path-containment.sh`.)
7. **Files under a stack directory** are written by temp file plus rename, as
   `writeEnvFileAtomic` (`backend/internal/handlers/env.go`) does, never by a
   bare `os.WriteFile`.
8. **Frontend mutations** that return an `ActionResult` go through
   `useActionMutation` (`frontend/src/hooks/useActionMutation.ts`). A `toast.success`
   that doesn't read the outcome is a defect.

## Trust and secrets

10. **Security fallbacks fail closed.** On error, return a 5xx or refuse. Never
    substitute a constant, a default-allow, or a looser check (the known instance is the CSP
    nonce fallback in `backend/cmd/server/main.go`, agent-os-n4ca.6).
11. **The credential the app issues wins**: the auth cookie first; an
    `Authorization` header only when no cookie is present and it is well-formed.
12. **Network position is not identity.** A bypass keyed on the peer address
    also checks the Host header. Trusted-proxy lists name the proxy, and
    templates and examples ship the narrow value, not all of RFC 1918.
    (CI-guarded: `scripts/check-trusted-networks.sh`.)
13. **A setting that holds or can embed a credential** is encrypted in the PR
    that adds it, by joining `sensitiveSettingKeys`
    (`backend/internal/database/settings.go`).

## Lifetimes

14. **Config loaded at boot is not written after boot.** A setting that changes
    it is persisted and applied at the next boot, as
    `ApplyPersistedDefaultStacksDir` (`backend/internal/config/config.go`) does.
15. **An operation's lifetime belongs to the operation**, not to the connection
    that started it: a compose run started over a WebSocket survives the tab
    closing. A long-lived connection re-checks the session it was opened under.
16. **An identity change resets per-identity client state**: logout and password
    change clear the query cache and lock the env-unlock store.

## Deploy artefacts

17. **Dev and prod compose agree** on the stacks mount (identical path), `init`,
    and env derivation, and a startup check validates the effect, not its
    inputs (`inspectStacksMount` in `backend/internal/config/config.go`).

Not here: rule 9 (numeric inputs mapping an empty field to 0) waits until its
sites are dispositioned; rule 18 (pin lockstep) is a CI guard (agent-os-qags.7);
rule 19 (user-facing text tied to a test) went to Agent OS; rule 20 (a review
finding is a claim) is covered by the close-time block in root `CLAUDE.md`.
