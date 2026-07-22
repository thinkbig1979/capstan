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

func (d *DB) GetUserByUsername(username string) (*models.User, error) {
	var user models.User
	query := `SELECT id, username, password, created_at, updated_at
	          FROM users WHERE username = ?`
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
