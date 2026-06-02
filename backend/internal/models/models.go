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

type ConfiguredDir struct {
	Path      string `json:"path"`
	Name      string `json:"name"`
	IsDefault bool   `json:"isDefault"`
}

type Directory struct {
	Path          string    `json:"path"`
	Name          string    `json:"name"`
	RootDir       string    `json:"rootDir,omitempty"`
	IsGitRepo     bool      `json:"isGitRepo"`
	GitRemote     string    `json:"gitRemote"`
	GitBranch     string    `json:"gitBranch"`
	GitAuthType   string    `json:"gitAuthType"`
	GitSSHKeyPath string    `json:"gitSshKeyPath,omitempty"`
	GitHTTPSUser  string    `json:"gitHttpsUser,omitempty"`
	GitHTTPSToken string    `json:"-"`
	HasHTTPSToken bool      `json:"hasHttpsToken"`
	ScannedAt     time.Time `json:"scannedAt"`
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
	MemSwap     float64 `json:"memSwap"`
	Pids        uint64  `json:"pids"`
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
	DirtyCount     int        `json:"dirtyCount"`
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

type DashboardContainerInfo struct {
	ID           string        `json:"id"`
	Name         string        `json:"name"`
	Image        string        `json:"image"`
	State        string        `json:"state"`
	Status       string        `json:"status"`
	Health       string        `json:"health"`
	Ports        []PortBinding `json:"ports"`
	StackID      string        `json:"stackId"`
	ProjectName  string        `json:"projectName"`
	RestartCount int           `json:"restartCount"`
	Created      time.Time     `json:"created"`
	StartedAt    time.Time     `json:"startedAt"`
	DiskSize     int64         `json:"diskSize"`
	ImageSize    int64         `json:"imageSize"`
}

type DashboardStats struct {
	TotalStacks       int                      `json:"totalStacks"`
	RunningStacks     int                      `json:"runningStacks"`
	StoppedStacks     int                      `json:"stoppedStacks"`
	TotalContainers   int                      `json:"totalContainers"`
	RunningContainers int                      `json:"runningContainers"`
	ImageDiskUsage    int64                    `json:"imageDiskUsage"`
	Containers        []DashboardContainerInfo `json:"containers"`
}

type DockerImage struct {
	ID         string   `json:"id"`
	RepoTags   []string `json:"repoTags"`
	Size       int64    `json:"size"`
	Created    int64    `json:"created"`
	Containers int      `json:"containers"`
}

type DockerVolume struct {
	Name       string `json:"name"`
	Driver     string `json:"driver"`
	Mountpoint string `json:"mountpoint"`
	Size       int64  `json:"size"`
	SizeKnown  bool   `json:"sizeKnown"`
	InUse      bool   `json:"inUse"`
	Created    string `json:"created"`
	Stack      string `json:"stack"`
}

type DockerNetwork struct {
	ID         string   `json:"id"`
	Name       string   `json:"name"`
	Driver     string   `json:"driver"`
	Scope      string   `json:"scope"`
	Internal   bool     `json:"internal"`
	Containers int      `json:"containers"`
	Labels     []string `json:"labels"`
	Created    string   `json:"created"`
	Stack      string   `json:"stack"`
}

type ContainerUpdateInfo struct {
	ContainerID   string `json:"containerId"`
	ContainerName string `json:"containerName"`
	Image         string `json:"image"`
	ImageRef      string `json:"imageRef"`
	State         string `json:"state"`
	StackID       string `json:"stackId"`
	ProjectName   string `json:"projectName"`
	ServiceName   string `json:"serviceName"`
	IsCompose     bool   `json:"isCompose"`
}

type StackEvent struct {
	Type        string    `json:"type"`
	StackID     string    `json:"stackId,omitempty"`
	ContainerID string    `json:"containerId,omitempty"`
	Event       string    `json:"event,omitempty"`
	Status      string    `json:"status,omitempty"`
	Timestamp   time.Time `json:"timestamp"`
	JobID       string    `json:"jobId,omitempty"`
	TargetType  string    `json:"targetType,omitempty"`
	TargetID    string    `json:"targetId,omitempty"`
	Name        string    `json:"name,omitempty"`
	JobError    string    `json:"error,omitempty"`
}

type CachedUpdate struct {
	ID            string `json:"id"`
	ContainerID   string `json:"containerId"`
	ContainerName string `json:"containerName"`
	Image         string `json:"image"`
	ImageRef      string `json:"imageRef"`
	State         string `json:"state"`
	StackID       string `json:"stackId,omitempty"`
	ProjectName   string `json:"projectName,omitempty"`
	ServiceName   string `json:"serviceName,omitempty"`
	IsCompose     bool   `json:"isCompose"`
	LocalDigest   string `json:"localDigest"`
	RemoteDigest  string `json:"remoteDigest"`
	ScannedAt     string `json:"scannedAt"`
}

type AutoUpdatePolicy struct {
	ID                  string `json:"id"`
	TargetType          string `json:"targetType"`
	TargetID            string `json:"targetId"`
	Enabled             bool   `json:"enabled"`
	ConsecutiveFailures int    `json:"consecutiveFailures"`
	Paused              bool   `json:"paused"`
	CreatedAt           string `json:"createdAt"`
	UpdatedAt           string `json:"updatedAt"`
}

type UpdateHistoryEntry struct {
	ID            string  `json:"id"`
	ContainerID   string  `json:"containerId"`
	ContainerName string  `json:"containerName"`
	StackID       *string `json:"stackId,omitempty"`
	StackName     *string `json:"stackName,omitempty"`
	Image         string  `json:"image"`
	OldDigest     *string `json:"oldDigest,omitempty"`
	NewDigest     *string `json:"newDigest,omitempty"`
	OldImageRef   *string `json:"oldImageRef,omitempty"`
	NewImageRef   *string `json:"newImageRef,omitempty"`
	Status        string  `json:"status"`
	Trigger       string  `json:"trigger"`
	StartedAt     string  `json:"startedAt"`
	CompletedAt   *string `json:"completedAt,omitempty"`
	DurationMs    *int64  `json:"durationMs,omitempty"`
	ErrorMessage  *string `json:"errorMessage,omitempty"`
}

type UpdateHistoryFilters struct {
	Page        int
	Limit       int
	Status      string
	Trigger     string
	ContainerID string
	StackID     string
	From        *time.Time
	To          *time.Time
}

type UpdateResult struct {
	HistoryID  string `json:"historyId"`
	OldDigest  string `json:"oldDigest"`
	NewDigest  string `json:"newDigest"`
	DurationMs int64  `json:"durationMs"`
}

type UpdateSettingsResponse struct {
	ScanIntervalMinutes int    `json:"scanIntervalMinutes"`
	LastScanAt          string `json:"lastScanAt,omitempty"`
	LastScanError       string `json:"lastScanError,omitempty"`
	GlobalAutoUpdate    bool   `json:"globalAutoUpdate"`
	AutoUpdateStats     struct {
		EnabledContainers int `json:"enabledContainers"`
		UpdatesLast7Days  int `json:"updatesLast7Days"`
		UpdatesLast30Days int `json:"updatesLast30Days"`
	} `json:"autoUpdateStats"`
}

type BackupPolicy struct {
	ID         string `json:"id"`
	TargetType string `json:"targetType"`
	TargetID   string `json:"targetId"`
	Enabled    bool   `json:"enabled"`
	StopPolicy string `json:"stopPolicy"` // "stop" | "hot"
	CreatedAt  string `json:"createdAt"`
	UpdatedAt  string `json:"updatedAt"`
}

type BackupRun struct {
	ID           string  `json:"id"`
	Kind         string  `json:"kind"`
	Trigger      string  `json:"trigger"`
	Status       string  `json:"status"`
	StartedAt    string  `json:"startedAt"`
	FinishedAt   *string `json:"finishedAt,omitempty"`
	StacksTotal  int     `json:"stacksTotal"`
	StacksOK     int     `json:"stacksOk"`
	StacksFailed int     `json:"stacksFailed"`
	BytesAdded   *int64  `json:"bytesAdded,omitempty"`
	ErrorMessage string  `json:"errorMessage,omitempty"`
}

type BackupRunItem struct {
	ID           string `json:"id"`
	RunID        string `json:"runId"`
	StackID      string `json:"stackId"`
	Status       string `json:"status"`
	SnapshotID   string `json:"snapshotId,omitempty"`
	StopApplied  bool   `json:"stopApplied"`
	DurationMs   int64  `json:"durationMs"`
	ErrorMessage string `json:"errorMessage,omitempty"`
}

type BackupSnapshot struct {
	ID        string   `json:"id"`
	ShortID   string   `json:"shortId"`
	Time      string   `json:"time"`
	Hostname  string   `json:"hostname"`
	Tags      []string `json:"tags"`
	Paths     []string `json:"paths"`
	SizeBytes int64    `json:"sizeBytes,omitempty"`
}
