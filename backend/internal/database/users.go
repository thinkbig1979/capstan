package database

import (
	"errors"
	"fmt"
	"time"

	"github.com/thinkbig1979/capstan/backend/internal/models"
)

func (d *DB) UserCount() (int, error) {
	var count int
	err := d.db.QueryRow("SELECT COUNT(*) FROM users").Scan(&count)
	return count, err
}

func (d *DB) CreateUser(user models.User) error {
	query := `INSERT INTO users (id, username, password, created_at, updated_at)
	          VALUES (?, ?, ?, ?, ?)`
	_, err := d.db.Exec(query, user.ID, user.Username, user.Password, user.CreatedAt, user.UpdatedAt)
	return err
}

// CreateFirstUser inserts the bootstrap admin only if no user exists yet, and
// reports whether it did. The insert and the "no users exist" test are one
// statement, so sqlite evaluates the WHERE NOT EXISTS while holding the write
// lock for that statement — two concurrent /auth/setup calls cannot both insert.
// This closes the TOCTOU that a UserCount()-then-CreateUser() sequence leaves
// open, where both callers read count==0 before either writes (agent-os-iut).
// A false return means someone else completed setup first; the caller should
// treat it as "setup already done", not an error.
func (d *DB) CreateFirstUser(user models.User) (bool, error) {
	query := `INSERT INTO users (id, username, password, created_at, updated_at)
	          SELECT ?, ?, ?, ?, ?
	          WHERE NOT EXISTS (SELECT 1 FROM users)`
	res, err := d.db.Exec(query, user.ID, user.Username, user.Password, user.CreatedAt, user.UpdatedAt)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return false, err
	}
	return n == 1, nil
}

// GetUserByUsername looks up a user case-insensitively (agent-os-tmo): the
// COLLATE NOCASE predicate matches the unique index migration 13 creates
// (idx_users_username_nocase), so "Admin" and "admin" resolve to the same
// row here the same way the index treats them as the same value on insert.
func (d *DB) GetUserByUsername(username string) (*models.User, error) {
	var user models.User
	query := `SELECT id, username, password, created_at, updated_at
	          FROM users WHERE username = ? COLLATE NOCASE`
	err := d.db.QueryRow(query, username).Scan(&user.ID, &user.Username, &user.Password, &user.CreatedAt, &user.UpdatedAt)
	if err != nil {
		return nil, err
	}
	return &user, nil
}

func (d *DB) GetUserByID(id string) (*models.User, error) {
	var user models.User
	query := `SELECT id, username, password, created_at, updated_at
	          FROM users WHERE id = ?`
	err := d.db.QueryRow(query, id).Scan(&user.ID, &user.Username, &user.Password, &user.CreatedAt, &user.UpdatedAt)
	if err != nil {
		return nil, err
	}
	return &user, nil
}

// ErrNoSoleUser reports that the users table does not hold exactly one row, so
// "the account" is ambiguous and the caller must name one explicitly.
var ErrNoSoleUser = errors.New("users table does not contain exactly one account")

// GetSoleUser returns the single account when the users table holds exactly
// one, and ErrNoSoleUser otherwise.
//
// This exists for the offline password reset (agent-os-8pa), where requiring
// --username would mean an operator locked out of their own instance has to
// remember which name they chose during first-run setup. Capstan permits
// exactly one account — /auth/setup 409s once userCount > 0 — so in practice
// there is never ambiguity to resolve. The error path is kept anyway rather
// than assuming the invariant holds in a database that may have been edited by
// hand, which for a recovery tool is exactly the situation to expect.
func (d *DB) GetSoleUser() (*models.User, error) {
	count, err := d.UserCount()
	if err != nil {
		return nil, err
	}
	if count != 1 {
		return nil, fmt.Errorf("%w: found %d", ErrNoSoleUser, count)
	}

	var user models.User
	query := `SELECT id, username, password, created_at, updated_at FROM users LIMIT 1`
	if err := d.db.QueryRow(query).Scan(
		&user.ID, &user.Username, &user.Password, &user.CreatedAt, &user.UpdatedAt,
	); err != nil {
		return nil, err
	}
	return &user, nil
}

func (d *DB) UpdateUserPassword(id, password string, updatedAt time.Time) error {
	query := `UPDATE users SET password = ?, updated_at = ? WHERE id = ?`
	_, err := d.db.Exec(query, password, updatedAt, id)
	return err
}

func (d *DB) CreateSession(session models.Session) error {
	query := `INSERT INTO sessions (id, user_id, expires_at, created_at)
	          VALUES (?, ?, ?, ?)`
	_, err := d.db.Exec(query, session.ID, session.UserID, session.ExpiresAt, session.CreatedAt)
	return err
}

func (d *DB) GetSession(id string) (*models.Session, error) {
	var session models.Session
	query := `SELECT id, user_id, expires_at, created_at
	          FROM sessions WHERE id = ?`
	err := d.db.QueryRow(query, id).Scan(&session.ID, &session.UserID, &session.ExpiresAt, &session.CreatedAt)
	if err != nil {
		return nil, err
	}
	return &session, nil
}

func (d *DB) DeleteSession(id string) error {
	query := `DELETE FROM sessions WHERE id = ?`
	_, err := d.db.Exec(query, id)
	return err
}

func (d *DB) DeleteSessionsByUserExcluding(userID, excludeSessionID string) error {
	query := `DELETE FROM sessions WHERE user_id = ? AND id != ?`
	_, err := d.db.Exec(query, userID, excludeSessionID)
	return err
}

// CountSessionsForUser reports how many session rows the user currently holds.
// Used by the offline password reset to verify its own revocation, and by
// tests that need to assert on session state rather than on a return code.
func (d *DB) CountSessionsForUser(userID string) (int, error) {
	var count int
	err := d.db.QueryRow("SELECT COUNT(*) FROM sessions WHERE user_id = ?", userID).Scan(&count)
	return count, err
}

func (d *DB) DeleteExpiredSessions() error {
	query := `DELETE FROM sessions WHERE expires_at < ?`
	_, err := d.db.Exec(query, time.Now())
	return err
}
