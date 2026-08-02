package database

import (
	"fmt"

	"github.com/thinkbig1979/capstan/backend/internal/models"
)

// UpsertDirectory records what a scan discovered about a directory: name,
// location, and its git remote/branch. It must never touch the credential
// columns (git_auth_type, git_ssh_key_path, git_https_user, git_https_token).
//
// This used to be `INSERT OR REPLACE` over all eleven columns, which deletes
// and rewrites the whole row on every scan — silently wiping any per-directory
// credential an operator had saved, since scans never populate those fields.
// The column-scoped upsert below makes "a scan does not own credentials"
// structural: credential columns are simply absent from the UPDATE SET, so
// there is nothing for a future column addition to accidentally clobber here.
func (d *DB) UpsertDirectory(dir models.Directory) error {
	query := `INSERT INTO directories (path, name, root_dir, is_git_repo, git_remote, git_branch, scanned_at)
	          VALUES (?, ?, ?, ?, ?, ?, ?)
	          ON CONFLICT(path) DO UPDATE SET
	              name = excluded.name,
	              root_dir = excluded.root_dir,
	              is_git_repo = excluded.is_git_repo,
	              git_remote = excluded.git_remote,
	              git_branch = excluded.git_branch,
	              scanned_at = excluded.scanned_at`
	_, err := d.db.Exec(query, dir.Path, dir.Name, dir.RootDir, dir.IsGitRepo, dir.GitRemote, dir.GitBranch, dir.ScannedAt)
	return err
}

// ListDirectories never returns a usable token: git_https_token is scanned
// straight from ciphertext (it is never decrypted here) and only tested for
// emptiness before being blanked into HasHTTPSToken. That test is valid
// without decrypting because Encrypt is only ever invoked on a non-empty
// plaintext, and UpdateDirectoryCredentials is the only writer that ever
// touches this column (UpsertDirectory no longer does), so the ciphertext
// column is empty if and only if the plaintext token was empty. Callers that
// need the real token must use GetDirectoryCredentials instead.
func (d *DB) ListDirectories() ([]models.Directory, error) {
	query := `SELECT path, name, root_dir, is_git_repo, git_remote, git_branch, git_auth_type, git_ssh_key_path, git_https_user, git_https_token, scanned_at
	          FROM directories ORDER BY name`
	rows, err := d.db.Query(query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	directories := make([]models.Directory, 0)
	for rows.Next() {
		var dir models.Directory
		err := rows.Scan(&dir.Path, &dir.Name, &dir.RootDir, &dir.IsGitRepo, &dir.GitRemote, &dir.GitBranch,
			&dir.GitAuthType, &dir.GitSSHKeyPath, &dir.GitHTTPSUser, &dir.GitHTTPSToken, &dir.ScannedAt)
		if err != nil {
			return nil, err
		}
		dir.HasHTTPSToken = dir.GitHTTPSToken != ""
		dir.GitHTTPSToken = ""
		directories = append(directories, dir)
	}
	return directories, nil
}

// GetDirectory has the same blank-on-read behaviour as ListDirectories; see
// its comment. Use GetDirectoryCredentials for the decrypted token.
func (d *DB) GetDirectory(path string) (*models.Directory, error) {
	var dir models.Directory
	query := `SELECT path, name, root_dir, is_git_repo, git_remote, git_branch, git_auth_type, git_ssh_key_path, git_https_user, git_https_token, scanned_at
	          FROM directories WHERE path = ?`
	err := d.db.QueryRow(query, path).Scan(&dir.Path, &dir.Name, &dir.RootDir, &dir.IsGitRepo, &dir.GitRemote, &dir.GitBranch,
		&dir.GitAuthType, &dir.GitSSHKeyPath, &dir.GitHTTPSUser, &dir.GitHTTPSToken, &dir.ScannedAt)
	if err != nil {
		return nil, err
	}
	dir.HasHTTPSToken = dir.GitHTTPSToken != ""
	dir.GitHTTPSToken = ""
	return &dir, nil
}

// GetDirectoryCredentials is the one reader that decrypts git_https_token. It
// exists so GitService can resolve a per-directory credential without going
// through ListDirectories/GetDirectory, which deliberately discard it.
func (d *DB) GetDirectoryCredentials(path string) (*models.Directory, error) {
	var dir models.Directory
	query := `SELECT path, git_auth_type, git_ssh_key_path, git_https_user, git_https_token
	          FROM directories WHERE path = ?`
	err := d.db.QueryRow(query, path).Scan(&dir.Path, &dir.GitAuthType, &dir.GitSSHKeyPath, &dir.GitHTTPSUser, &dir.GitHTTPSToken)
	if err != nil {
		return nil, err
	}
	if d.encryptor != nil && dir.GitHTTPSToken != "" {
		decrypted, err := d.encryptor.Decrypt(dir.GitHTTPSToken)
		if err != nil {
			return nil, fmt.Errorf("failed to decrypt token: %w", err)
		}
		dir.GitHTTPSToken = decrypted
	}
	return &dir, nil
}

func (d *DB) DeleteDirectory(path string) error {
	query := `DELETE FROM directories WHERE path = ?`
	_, err := d.db.Exec(query, path)
	return err
}

func (d *DB) UpdateDirectoryCredentials(path, authType, sshKeyPath, httpsUser, httpsToken string) error {
	if d.encryptor != nil && httpsToken != "" {
		encrypted, err := d.encryptor.Encrypt(httpsToken)
		if err != nil {
			return fmt.Errorf("failed to encrypt token: %w", err)
		}
		httpsToken = encrypted
	}
	query := `UPDATE directories SET git_auth_type = ?, git_ssh_key_path = ?, git_https_user = ?, git_https_token = ? WHERE path = ?`
	_, err := d.db.Exec(query, authType, sshKeyPath, httpsUser, httpsToken, path)
	return err
}

func (d *DB) ClearDirectories() error {
	query := `DELETE FROM directories`
	_, err := d.db.Exec(query)
	return err
}
