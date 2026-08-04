package database

import (
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

func (d *DB) DeleteExpiredSessions() error {
	query := `DELETE FROM sessions WHERE expires_at < ?`
	_, err := d.db.Exec(query, time.Now())
	return err
}
