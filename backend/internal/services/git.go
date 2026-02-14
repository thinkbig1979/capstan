package services

import (
	"fmt"
	"log/slog"
	"path/filepath"
	"strings"
	"time"

	"github.com/docker-manager/backend/internal/config"
	"github.com/docker-manager/backend/internal/models"
	"github.com/go-git/go-billy/v5/osfs"
	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/object"
	"github.com/go-git/go-git/v5/plumbing/transport"
	"github.com/go-git/go-git/v5/plumbing/transport/http"
	"github.com/go-git/go-git/v5/plumbing/transport/ssh"
	"github.com/go-git/go-git/v5/storage/filesystem"
)

type GitService struct {
	config *config.Config
}

func NewGitService(cfg *config.Config) *GitService {
	return &GitService{
		config: cfg,
	}
}

func (s *GitService) GetStatus(dirPath string) (*models.GitStatusResult, error) {
	slog.Debug("Getting git status", "path", dirPath)

	repo, err := s.openRepo(dirPath)
	if err != nil {
		return nil, err
	}

	head, err := repo.Head()
	if err != nil {
		return nil, fmt.Errorf("failed to get HEAD: %w", err)
	}

	branchName := head.Name().Short()
	commitHash := head.Hash()

	commit, err := repo.CommitObject(commitHash)
	if err != nil {
		return nil, fmt.Errorf("failed to get commit: %w", err)
	}

	gitCommit := s.mapCommit(commit)

	worktree, err := repo.Worktree()
	if err != nil {
		return nil, fmt.Errorf("failed to get worktree: %w", err)
	}

	status, err := worktree.Status()
	if err != nil {
		return nil, fmt.Errorf("failed to get status: %w", err)
	}

	dirty := !status.IsClean()

	ahead, behind, trackingBranch, remoteURL, err := s.getDivergence(repo, head)
	if err != nil {
		slog.Warn("Failed to get ahead/behind status", "error", err)
		ahead = 0
		behind = 0
		trackingBranch = ""
		remoteURL = ""
	}

	return &models.GitStatusResult{
		Branch:         branchName,
		Commit:         gitCommit,
		Dirty:          dirty,
		Ahead:          ahead,
		Behind:         behind,
		RemoteURL:      remoteURL,
		TrackingBranch: trackingBranch,
	}, nil
}

func (s *GitService) Pull(dirPath string) (*models.PullResult, error) {
	slog.Debug("Pulling git changes", "path", dirPath)

	repo, err := s.openRepo(dirPath)
	if err != nil {
		return nil, err
	}

	worktree, err := repo.Worktree()
	if err != nil {
		return nil, fmt.Errorf("failed to get worktree: %w", err)
	}

	status, err := worktree.Status()
	if err != nil {
		return nil, fmt.Errorf("failed to get status: %w", err)
	}

	if !status.IsClean() {
		return nil, models.NewAppError(400, models.ErrGitDirty, "Working directory has uncommitted changes")
	}

	head, err := repo.Head()
	if err != nil {
		return nil, fmt.Errorf("failed to get HEAD: %w", err)
	}

	previousCommit := head.Hash().String()

	auth, err := s.getAuthMethod(dirPath)
	if err != nil {
		return nil, fmt.Errorf("failed to get auth: %w", err)
	}

	pullOpts := &git.PullOptions{
		RemoteName:      "origin",
		SingleBranch:    true,
		Force:           false,
		Progress:        nil,
		Auth:            auth,
		InsecureSkipTLS: false,
	}

	if auth == nil {
		pullOpts.Auth = s.getPublicAuth()
	}

	if err := worktree.Pull(pullOpts); err != nil {
		if err == transport.ErrEmptyRemoteRepository {
			return nil, models.NewAppError(400, models.ErrGitConflict, "Remote repository is empty")
		}
		if err == git.NoErrAlreadyUpToDate {
			return &models.PullResult{
				PreviousCommit: previousCommit,
				CurrentCommit:  previousCommit,
				ChangedFiles:   []string{},
			}, nil
		}
		return nil, models.NewAppErrorWithDetails(500, models.ErrGitConflict, "Failed to pull", err.Error())
	}

	head, err = repo.Head()
	if err != nil {
		return nil, fmt.Errorf("failed to get new HEAD: %w", err)
	}

	currentCommit := head.Hash().String()

	changedFiles, err := s.getChangedFiles(repo, previousCommit, currentCommit)
	if err != nil {
		slog.Warn("Failed to get changed files", "error", err)
		changedFiles = []string{}
	}

	return &models.PullResult{
		PreviousCommit: previousCommit,
		CurrentCommit:  currentCommit,
		ChangedFiles:   changedFiles,
	}, nil
}

func (s *GitService) GetLog(dirPath string, limit, offset int) (*models.LogResult, error) {
	if limit <= 0 {
		limit = 50
	}
	if limit > 200 {
		limit = 200
	}

	slog.Debug("Getting git log", "path", dirPath, "limit", limit, "offset", offset)

	repo, err := s.openRepo(dirPath)
	if err != nil {
		return nil, err
	}

	head, err := repo.Head()
	if err != nil {
		return nil, fmt.Errorf("failed to get HEAD: %w", err)
	}

	commit, err := repo.CommitObject(head.Hash())
	if err != nil {
		return nil, fmt.Errorf("failed to get commit: %w", err)
	}

	commits := []models.GitCommit{}
	count := 0
	total := 0
	current := commit

	for current != nil {
		total++
		if count < offset {
			current = s.getParent(current)
			continue
		}

		if len(commits) >= limit {
			break
		}

		commits = append(commits, *s.mapCommit(current))
		count++

		current = s.getParent(current)
	}

	hasMore := false
	if current != nil {
		hasMore = true
	}

	return &models.LogResult{
		Commits: commits,
		Total:   total,
		HasMore: hasMore,
	}, nil
}

func (s *GitService) GetDiff(dirPath string, commitHash string) (*models.DiffResult, error) {
	slog.Debug("Getting git diff", "path", dirPath, "commit", commitHash)

	repo, err := s.openRepo(dirPath)
	if err != nil {
		return nil, err
	}

	hash := plumbing.NewHash(commitHash)
	commit, err := repo.CommitObject(hash)
	if err != nil {
		return nil, fmt.Errorf("failed to get commit: %w", err)
	}

	parent := s.getParent(commit)

	var patch *object.Patch
	var files []string

	if parent != nil {
		patch, err = commit.Patch(parent)
		if err != nil {
			return nil, fmt.Errorf("failed to generate patch: %w", err)
		}

		filePatches := patch.FilePatches()
		for _, filePatch := range filePatches {
			from, to := filePatch.Files()
			if from != nil {
				files = append(files, from.Path())
			}
			if to != nil && (from == nil || from.Path() != to.Path()) {
				files = append(files, to.Path())
			}
		}
	} else {
		tree, err := commit.Tree()
		if err != nil {
			return nil, fmt.Errorf("failed to get tree: %w", err)
		}

		tree.Files().ForEach(func(f *object.File) error {
			files = append(files, f.Name)
			return nil
		})

		patch = nil
	}

	diffStr := ""
	if patch != nil {
		diffStr = patch.String()
	}

	return &models.DiffResult{
		Commit: s.mapCommit(commit),
		Diff:   diffStr,
		Files:  files,
	}, nil
}

func (s *GitService) GetLogForFile(dirPath, filePath string, limit int) (*models.LogResult, error) {
	if limit <= 0 {
		limit = 50
	}
	if limit > 200 {
		limit = 200
	}

	slog.Debug("Getting git log for file", "path", dirPath, "file", filePath, "limit", limit)

	repo, err := s.openRepo(dirPath)
	if err != nil {
		return nil, err
	}

	head, err := repo.Head()
	if err != nil {
		return nil, fmt.Errorf("failed to get HEAD: %w", err)
	}

	headCommit, err := repo.CommitObject(head.Hash())
	if err != nil {
		return nil, fmt.Errorf("failed to get commit: %w", err)
	}

	commits := []models.GitCommit{}
	current := headCommit

	for current != nil {
		if len(commits) >= limit {
			break
		}

		affected, err := s.fileAffectedByCommit(current, filePath)
		if err != nil {
			slog.Warn("Failed to check if file affected", "error", err)
		}

		if affected {
			commits = append(commits, *s.mapCommit(current))
		}

		current = s.getParent(current)
	}

	return &models.LogResult{
		Commits: commits,
		Total:   len(commits),
		HasMore: false,
	}, nil
}

func (s *GitService) openRepo(dirPath string) (*git.Repository, error) {
	fs := osfs.New(dirPath)
	stor := filesystem.NewStorage(fs, nil)
	repo, err := git.Open(stor, fs)
	if err != nil {
		if err == git.ErrRepositoryNotExists {
			return nil, models.NewAppError(404, models.ErrGitNotRepo, "Not a git repository")
		}
		return nil, fmt.Errorf("failed to open repository: %w", err)
	}
	return repo, nil
}

func (s *GitService) getDivergence(repo *git.Repository, head *plumbing.Reference) (ahead, behind int, trackingBranch, remoteURL string, err error) {
	remoteName := "origin"
	branchName := head.Name().Short()
	trackingBranch = fmt.Sprintf("%s/%s", remoteName, branchName)

	remoteRef, err := repo.Storer.Reference(plumbing.ReferenceName(fmt.Sprintf("refs/remotes/%s/%s", remoteName, branchName)))
	if err != nil {
		if err == plumbing.ErrReferenceNotFound {
			return 0, 0, "", "", nil
		}
		return 0, 0, "", "", fmt.Errorf("failed to get remote ref: %w", err)
	}

	cfg, err := repo.Config()
	if err != nil {
		return 0, 0, "", "", fmt.Errorf("failed to get config: %w", err)
	}

	remoteURL = ""
	for name, remote := range cfg.Remotes {
		if name == remoteName && len(remote.URLs) > 0 {
			remoteURL = remote.URLs[0]
			break
		}
	}

	localCommit, err := repo.CommitObject(head.Hash())
	if err != nil {
		return 0, 0, "", "", fmt.Errorf("failed to get local commit: %w", err)
	}

	remoteCommit, err := repo.CommitObject(remoteRef.Hash())
	if err != nil {
		return 0, 0, "", "", fmt.Errorf("failed to get remote commit: %w", err)
	}

	mergeBase, err := s.findMergeBase(repo, localCommit, remoteCommit)
	if err != nil {
		return 0, 0, "", "", fmt.Errorf("failed to find merge base: %w", err)
	}

	ahead = s.countCommits(repo, mergeBase.Hash, localCommit.Hash)
	behind = s.countCommits(repo, mergeBase.Hash, remoteCommit.Hash)

	return ahead, behind, trackingBranch, remoteURL, nil
}

func (s *GitService) findMergeBase(repo *git.Repository, commit1, commit2 *object.Commit) (*object.Commit, error) {
	commits1 := s.collectAncestors(repo, commit1)
	commits2 := s.collectAncestors(repo, commit2)

	for hash := range commits1 {
		if commits2[hash] {
			return repo.CommitObject(hash)
		}
	}

	return nil, fmt.Errorf("no merge base found")
}

func (s *GitService) collectAncestors(repo *git.Repository, commit *object.Commit) map[plumbing.Hash]bool {
	ancestors := map[plumbing.Hash]bool{}
	ancestors[commit.Hash] = true

	queue := []*object.Commit{commit}

	for len(queue) > 0 {
		current := queue[0]
		queue = queue[1:]

		parents := current.Parents()
		parents.ForEach(func(parent *object.Commit) error {
			if !ancestors[parent.Hash] {
				ancestors[parent.Hash] = true
				queue = append(queue, parent)
			}
			return nil
		})
	}

	return ancestors
}

func (s *GitService) countCommits(repo *git.Repository, base, target plumbing.Hash) int {
	count := 0
	current, err := repo.CommitObject(target)
	if err != nil {
		return 0
	}

	for current != nil && current.Hash != base {
		count++
		current = s.getParent(current)
	}

	return count
}

func (s *GitService) getChangedFiles(repo *git.Repository, oldCommitHash, newCommitHash string) ([]string, error) {
	oldHash := plumbing.NewHash(oldCommitHash)
	newHash := plumbing.NewHash(newCommitHash)

	newCommit, err := repo.CommitObject(newHash)
	if err != nil {
		return nil, err
	}

	oldCommit, err := repo.CommitObject(oldHash)
	if err != nil {
		return nil, err
	}

	patch, err := newCommit.Patch(oldCommit)
	if err != nil {
		return nil, err
	}

	files := []string{}
	for _, filePatch := range patch.FilePatches() {
		from, to := filePatch.Files()
		if from != nil {
			files = append(files, from.Path())
		} else if to != nil {
			files = append(files, to.Path())
		}
	}

	return files, nil
}

func (s *GitService) fileAffectedByCommit(commit *object.Commit, filePath string) (bool, error) {
	parent := s.getParent(commit)
	if parent == nil {
		return true, nil
	}

	patch, err := commit.Patch(parent)
	if err != nil {
		return false, err
	}

	normalizedPath := filepath.ToSlash(filePath)
	if !strings.HasPrefix(normalizedPath, "/") {
		normalizedPath = "/" + normalizedPath
	}

	for _, filePatch := range patch.FilePatches() {
		from, to := filePatch.Files()

		if from != nil && filepath.ToSlash(from.Path()) == normalizedPath {
			return true, nil
		}
		if to != nil && filepath.ToSlash(to.Path()) == normalizedPath {
			return true, nil
		}
	}

	return false, nil
}

func (s *GitService) getAuthMethod(dirPath string) (transport.AuthMethod, error) {
	repo, err := s.openRepo(dirPath)
	if err != nil {
		return nil, err
	}

	cfg, err := repo.Config()
	if err != nil {
		return nil, fmt.Errorf("failed to get config: %w", err)
	}

	for name, remote := range cfg.Remotes {
		if name == "origin" && len(remote.URLs) > 0 {
			remoteURL := remote.URLs[0]

			if strings.HasPrefix(remoteURL, "git@") || strings.HasPrefix(remoteURL, "ssh://") {
				if s.config.GitSSHKey != "" {
					publicKeys, err := ssh.NewPublicKeysFromFile("git", s.config.GitSSHKey, "")
					if err != nil {
						slog.Warn("Failed to load SSH key", "error", err)
						return nil, nil
					}
					return publicKeys, nil
				}

				sshAuth, err := ssh.NewSSHAgentAuth("git")
				if err == nil {
					return sshAuth, nil
				}

				return nil, nil
			}

			if strings.HasPrefix(remoteURL, "https://") || strings.HasPrefix(remoteURL, "http://") {
				if s.config.GitHTTPSToken != "" {
					return &http.BasicAuth{
						Username: s.config.GitHTTPSUser,
						Password: s.config.GitHTTPSToken,
					}, nil
				}

				return nil, nil
			}
		}
	}

	return nil, nil
}

func (s *GitService) getPublicAuth() transport.AuthMethod {
	return &http.BasicAuth{
		Username: "anything",
		Password: "",
	}
}

func (s *GitService) mapCommit(commit *object.Commit) *models.GitCommit {
	return &models.GitCommit{
		Hash:    commit.Hash.String(),
		Short:   commit.Hash.String()[:7],
		Author:  commit.Author.Name,
		Email:   commit.Author.Email,
		Message: strings.Split(commit.Message, "\n")[0],
		Date:    commit.Author.When.Format(time.RFC3339),
	}
}

func (s *GitService) getParent(commit *object.Commit) *object.Commit {
	parents := commit.Parents()
	parent, err := parents.Next()
	if err != nil {
		return nil
	}
	return parent
}
