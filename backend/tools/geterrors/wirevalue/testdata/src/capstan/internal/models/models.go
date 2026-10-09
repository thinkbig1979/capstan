package models

type Stack struct {
	ID     string
	Status string
}

type StackEvent struct {
	Type       string
	Status     string
	TargetType string
}

type AutoUpdatePolicy struct {
	TargetType string
	TargetID   string
}

// BackupRun.Status shares "running" and "partial" with a stack status; it is
// the reason this is an analyzer and not a grep, so it must never fire.
type BackupRun struct {
	Status string
}
