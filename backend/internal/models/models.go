package models

import "time"

type User struct {
	ID        string    `json:"id"`
	Username  string    `json:"username"`
	Password  string    `json:"-"`
	CreatedAt time.Time `json:"createdAt"`
	UpdatedAt time.Time `json:"updatedAt"`
}

type Session struct {
	ID        string    `json:"id"`
	UserID    string    `json:"userId"`
	ExpiresAt time.Time `json:"expiresAt"`
	CreatedAt time.Time `json:"createdAt"`
}

type Directory struct {
	Path      string    `json:"path"`
	Name      string    `json:"name"`
	IsGitRepo bool      `json:"isGitRepo"`
	GitRemote string    `json:"gitRemote"`
	GitBranch string    `json:"gitBranch"`
	ScannedAt time.Time `json:"scannedAt"`
}

type Stack struct {
	ID          string      `json:"id"`
	Directory   string      `json:"directory"`
	ComposeFile string      `json:"composeFile"`
	EnvFile     string      `json:"envFile"`
	ProjectName string      `json:"projectName"`
	Status      string      `json:"status"`
	Containers  []Container `json:"containers"`
	IsGitRepo   bool        `json:"isGitRepo"`
	GitBranch   string      `json:"gitBranch"`
	GitCommit   string      `json:"gitCommit"`
	GitDirty    bool        `json:"gitDirty"`
	GitAhead    int         `json:"gitAhead"`
	GitBehind   int         `json:"gitBehind"`
}

type Container struct {
	ID     string        `json:"id"`
	Name   string        `json:"name"`
	Image  string        `json:"image"`
	State  string        `json:"state"`
	Status string        `json:"status"`
	Ports  []PortBinding `json:"ports"`
	Health string        `json:"health"`
}

type PortBinding struct {
	Host      string `json:"host"`
	Container string `json:"container"`
	Protocol  string `json:"protocol"`
}

type ContainerMetrics struct {
	ContainerID string  `json:"containerId"`
	Name        string  `json:"name"`
	CPUPercent  float64 `json:"cpuPercent"`
	MemUsage    float64 `json:"memUsage"`
	MemLimit    float64 `json:"memLimit"`
	MemPercent  float64 `json:"memPercent"`
	NetRx       float64 `json:"netRx"`
	NetTx       float64 `json:"netTx"`
	BlockRead   float64 `json:"blockRead"`
	BlockWrite  float64 `json:"blockWrite"`
}

type GitCommit struct {
	Hash    string `json:"hash"`
	Short   string `json:"short"`
	Author  string `json:"author"`
	Email   string `json:"email"`
	Message string `json:"message"`
	Date    string `json:"date"`
}

type LintResult struct {
	Level   string `json:"level"`
	Line    int    `json:"line"`
	Message string `json:"message"`
	Rule    string `json:"rule"`
}

type ActionLog struct {
	ID        string    `json:"id"`
	UserID    string    `json:"userId"`
	StackID   string    `json:"stackId"`
	Action    string    `json:"action"`
	Detail    string    `json:"detail"`
	CreatedAt time.Time `json:"createdAt"`
}

type CommandResult struct {
	ExitCode int    `json:"exitCode"`
	Stdout   string `json:"stdout"`
	Stderr   string `json:"stderr"`
}

type DockerEvent struct {
	ContainerID string    `json:"containerId"`
	Action      string    `json:"action"`
	Type        string    `json:"type"`
	Timestamp   time.Time `json:"timestamp"`
}

type GitStatusResult struct {
	Branch         string     `json:"branch"`
	Commit         *GitCommit `json:"commit"`
	Dirty          bool       `json:"dirty"`
	Ahead          int        `json:"ahead"`
	Behind         int        `json:"behind"`
	RemoteURL      string     `json:"remoteUrl"`
	TrackingBranch string     `json:"trackingBranch"`
}

type PullResult struct {
	PreviousCommit string   `json:"previousCommit"`
	CurrentCommit  string   `json:"currentCommit"`
	ChangedFiles   []string `json:"changedFiles"`
}

type LogResult struct {
	Commits []GitCommit `json:"commits"`
	Total   int         `json:"total"`
	HasMore bool        `json:"hasMore"`
}

type DiffResult struct {
	Commit *GitCommit `json:"commit"`
	Diff   string     `json:"diff"`
	Files  []string   `json:"files"`
}

type StackEvent struct {
	Type        string    `json:"type"`
	StackID     string    `json:"stackId,omitempty"`
	ContainerID string    `json:"containerId,omitempty"`
	Event       string    `json:"event,omitempty"`
	Status      string    `json:"status,omitempty"`
	Timestamp   time.Time `json:"timestamp"`
}
