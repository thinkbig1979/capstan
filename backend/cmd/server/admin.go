package main

// Offline administrative commands, dispatched before the server starts.
//
// WHY THIS EXISTS (agent-os-8pa)
//
// Capstan permits exactly one account: POST /api/v1/auth/setup returns 409 once
// userCount > 0, and there is no password-reset endpoint, no recovery flow and
// no second account to fall back on. Losing the admin password therefore meant
// hand-editing a bcrypt hash in capstan.db, or deleting the database and
// redoing first-run setup — losing every stack history, setting and stored
// credential with it. For the tool that manages every stack on a host, that is
// a poor failure mode.
//
// WHY AN OFFLINE COMMAND RATHER THAN AN ENDPOINT
//
// This grants no privilege that its caller does not already hold. Running it
// requires shell access to the container, and anyone with that can already read
// JWT_SECRET from the environment and mint a token, or edit the database
// directly. So it is ergonomics over what host access already permits, not a
// new way in — which is precisely the argument a network-facing reset flow
// cannot make. It is also why the command deliberately has no authentication of
// its own: there is nothing left to authenticate against once you are inside
// the trust boundary it sits behind.
//
// The password is read from stdin and never from argv, because a process's
// arguments are readable by every other process on the host and land in shell
// history besides.

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"golang.org/x/crypto/bcrypt"

	"github.com/thinkbig1979/capstan/backend/internal/database"
	"github.com/thinkbig1979/capstan/backend/internal/middleware"
	"github.com/thinkbig1979/capstan/backend/internal/models"
)

const adminUsage = `Usage: server admin <command> [flags]

Commands:
  reset-password    Set a new password for an account without knowing the old one.

Flags for reset-password:
  --username <name>   Account to reset. Optional when only one account exists.
  --data-dir <path>   Directory holding capstan.db. Defaults to $DATA_DIR, then /app/data.

The new password is read from stdin, never from a flag, so it does not appear
in the process list or your shell history. Type it without echo like this:

  read -rs NEWPW && printf '%s' "$NEWPW" | \
    docker compose exec -T capstan /app/server admin reset-password

All of the account's sessions are revoked as part of the reset, so anyone
holding a login cookie is signed out. The server does not need restarting.
`

// runAdminCommand executes an "admin" subcommand and returns a process exit
// code. All I/O is injected so the behaviour can be tested without a terminal
// or a live server.
func runAdminCommand(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		_, _ = io.WriteString(stderr, adminUsage)
		return 2
	}

	switch args[0] {
	case "reset-password":
		return runResetPassword(args[1:], stdin, stdout, stderr)
	case "-h", "--help", "help":
		_, _ = io.WriteString(stdout, adminUsage)
		return 0
	default:
		fmt.Fprintf(stderr, "unknown admin command %q\n\n%s", args[0], adminUsage)
		return 2
	}
}

func runResetPassword(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("reset-password", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.Usage = func() { _, _ = io.WriteString(stderr, adminUsage) }
	username := fs.String("username", "", "account to reset (optional when only one exists)")
	dataDir := fs.String("data-dir", "", "directory holding capstan.db")
	if err := fs.Parse(args); err != nil {
		return 2
	}

	resolvedDir := resolveDataDir(*dataDir)

	// Check for the file before opening it. database.New creates the directory
	// and an empty database when they are absent, so a mistyped --data-dir
	// would otherwise produce a brand-new empty instance and a confusing "no
	// accounts" error — which to an operator already locked out reads like
	// their data has vanished.
	dbPath := filepath.Join(resolvedDir, "capstan.db")
	if _, err := os.Stat(dbPath); err != nil {
		fmt.Fprintf(stderr, "no capstan.db at %s\n"+
			"Point --data-dir at the directory holding it (inside the container this is /app/data).\n", dbPath)
		return 1
	}

	// Read the password before touching the database, so a malformed or empty
	// input cannot leave a half-applied reset behind.
	password, err := readPassword(stdin)
	if err != nil {
		fmt.Fprintf(stderr, "%v\n\n%s", err, adminUsage)
		return 2
	}

	// The same rules the signup and change-password paths enforce. A recovery
	// route that accepted weaker passwords would quietly become the weakest way
	// to hold an account.
	if ok, msg := middleware.ValidatePassword(password); !ok {
		fmt.Fprintf(stderr, "refusing to set this password: %s\n", msg)
		return 1
	}

	// Migrations are deliberately not run here: a recovery tool should read and
	// write two rows, not restructure a database whose state you are already
	// unsure about. The server applies migrations at startup as it always has.
	db, err := database.New(resolvedDir)
	if err != nil {
		fmt.Fprintf(stderr, "open database: %v\n", err)
		return 1
	}
	defer db.Close()

	user, err := resolveAccount(db, *username)
	if err != nil {
		fmt.Fprintf(stderr, "%v\n", err)
		return 1
	}

	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		fmt.Fprintf(stderr, "hash password: %v\n", err)
		return 1
	}

	if err := db.UpdateUserPassword(user.ID, string(hash), time.Now().UTC()); err != nil {
		fmt.Fprintf(stderr, "update password: %v\n", err)
		return 1
	}

	// Revoke every session, not just the others. A reset is most often reached
	// for because access may be compromised, and leaving a valid cookie alive
	// would defeat it. Passing "" as the exclusion matches no session ID, so
	// this clears them all. Sessions are checked against the database on each
	// request, so this takes effect without restarting the server.
	//
	// Reported rather than fatal: the password is already changed by this point,
	// and exiting non-zero here would suggest to the operator that the reset did
	// not happen when it did.
	if err := db.DeleteSessionsByUserExcluding(user.ID, ""); err != nil {
		fmt.Fprintf(stderr, "WARNING: password was reset but sessions could not be revoked: %v\n"+
			"Restart the container to invalidate them.\n", err)
		return 1
	}

	remaining, err := db.CountSessionsForUser(user.ID)
	if err == nil && remaining > 0 {
		fmt.Fprintf(stderr, "WARNING: password was reset but %d session(s) remain.\n", remaining)
		return 1
	}

	fmt.Fprintf(stdout, "Password reset for %q. All sessions revoked; sign in again.\n", user.Username)
	return 0
}

// resolveDataDir mirrors config.Load's DATA_DIR handling. config itself is not
// reused because it treats JWT_SECRET as a hard startup requirement, and a
// password reset neither mints nor verifies tokens — demanding a secret to
// recover an account would be an unnecessary way for this to fail.
func resolveDataDir(flagValue string) string {
	if flagValue != "" {
		return flagValue
	}
	if env := os.Getenv("DATA_DIR"); env != "" {
		return env
	}
	return "/app/data"
}

func resolveAccount(db *database.DB, username string) (*models.User, error) {
	if username != "" {
		user, err := db.GetUserByUsername(username)
		if err != nil {
			return nil, fmt.Errorf("no account named %q: %w", username, err)
		}
		return user, nil
	}

	user, err := db.GetSoleUser()
	if errors.Is(err, database.ErrNoSoleUser) {
		return nil, fmt.Errorf("%w; name one with --username", err)
	}
	if err != nil {
		return nil, fmt.Errorf("look up account: %w", err)
	}
	return user, nil
}

// readPassword takes the first line of stdin. A terminal is rejected outright
// rather than prompted: reading a password from a TTY without echo needs a
// dependency this binary does not otherwise carry, and echoing one to the
// screen is worse than refusing. The usage text shows the `read -rs` form that
// keeps it off both the screen and the shell history.
func readPassword(stdin io.Reader) (string, error) {
	if f, ok := stdin.(*os.File); ok {
		if info, err := f.Stat(); err == nil && info.Mode()&os.ModeCharDevice != 0 {
			return "", errors.New("refusing to read a password from a terminal; pipe it on stdin instead")
		}
	}

	data, err := io.ReadAll(stdin)
	if err != nil {
		return "", fmt.Errorf("read password from stdin: %w", err)
	}

	// Only the first line, so a trailing newline from `read`/`echo` is not
	// taken as part of the password.
	password := strings.SplitN(string(data), "\n", 2)[0]
	password = strings.TrimSuffix(password, "\r")
	if password == "" {
		return "", errors.New("no password on stdin")
	}
	return password, nil
}
