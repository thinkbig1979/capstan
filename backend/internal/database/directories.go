package database

import (
	"fmt"

	"github.com/thinkbig1979/capstan/backend/internal/models"
)

func (d *DB) UpsertDirectory(dir models.Directory) error {
	token := dir.GitHTTPSToken
	if d.encryptor != nil && token != "" {
		encrypted, err := d.encryptor.Encrypt(token)
		if err != nil {
			return fmt.Errorf("failed to encrypt token: %w", err)
		}
		token = encrypted
	}
	query := `INSERT OR REPLACE INTO directories
	          (path, name, root_dir, is_git_repo, git_remote, git_branch, git_auth_type, git_ssh_key_path, git_https_user, git_https_token, scanned_at)
	          VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`
	_, err := d.db.Exec(query, dir.Path, dir.Name, dir.RootDir, dir.IsGitRepo, dir.GitRemote, dir.GitBranch,
		dir.GitAuthType, dir.GitSSHKeyPath, dir.GitHTTPSUser, token, dir.ScannedAt)
	return err
}

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
