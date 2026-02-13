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
	MemUsage    string  `json:"memUsage"`
	MemLimit    string  `json:"memLimit"`
	MemPercent  float64 `json:"memPercent"`
	NetRx       string  `json:"netRx"`
	NetTx       string  `json:"netTx"`
	BlockRead   string  `json:"blockRead"`
	BlockWrite  string  `json:"blockWrite"`
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
