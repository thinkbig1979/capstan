package services

import (
	"strings"

	"capstan/internal/models"
)

type StackStatus string

const (
	StackStatusRunning StackStatus = "running"
	StackStatusStopped StackStatus = "stopped"
	StackStatusPartial StackStatus = "partial"
)

type JobTargetType string

const (
	JobTargetTypeContainer JobTargetType = "container"
	JobTargetTypeStack     JobTargetType = "stack"
)

// plainRunning is a constant of plain string type: a literal by another name.
const plainRunning = "running"

type LiveStatus struct {
	Status string
}

type DockerService struct{}

// Status is a seeded result: its local spreads the mark to every literal.
func (s *DockerService) Status(stack models.Stack) (string, error) {
	return parse("")
}

func parse(out string) (string, error) {
	status := "running" // want `bare "running" is assigned to a services.StackStatus carrier \(status\): use string\(services.StackStatusRunning\)`
	if out == "" {
		status = string(StackStatusStopped) // the typed constant: not reported
	}
	return status, nil
}

type dockerStopper interface {
	Status(stack models.Stack) (string, error)
}

func observe(d dockerStopper, stack models.Stack) bool {
	status, err := d.Status(stack)
	if err != nil {
		return false
	}
	return status == "running" // want `bare "running" is compared with a services.StackStatus carrier \(status\)`
}

// Field assignment, composite key, a plain-string const, and a switch case.
func events(ev *models.StackEvent, live map[string]LiveStatus) {
	ev.Status = "stopped"                        // want `bare "stopped" is assigned to a services.StackStatus carrier \(Status\)`
	live["p"] = LiveStatus{Status: plainRunning} // want `bare "running" is set as a services.StackStatus carrier \(Status\)`
	switch ev.Status {
	case "partial": // want `bare "partial" is compared with a services.StackStatus carrier \(Status\)`
	case string(StackStatusRunning):
	}
	ev.TargetType = "stack" // want `bare "stack" is assigned to a services.JobTargetType carrier \(TargetType\)`
	ev.Type = "running"     // not a carrier field
}

// Call argument to a same-package parameter: the parameter becomes a carrier
// because the caller hands it a seeded value.
func setStatus(stack *models.Stack, s string) { stack.Status = s }

func callers(stack *models.Stack) {
	setStatus(stack, "partial") // want `bare "partial" is passed as a services.StackStatus carrier \(s\)`
}

// Policy targets: the scheduler's switch.
func policies(ps []models.AutoUpdatePolicy) {
	for _, p := range ps {
		switch p.TargetType {
		case "container": // want `bare "container" is compared with a services.JobTargetType carrier \(TargetType\)`
		case string(JobTargetTypeStack):
		}
	}
}

// NEGATIVES. A backup run's status is a different set on a different field.
func backups(run *models.BackupRun, label string) {
	run.Status = "running"
	if run.Status == "partial" {
		run.Status = "error"
	}
	// A cross-package helper's parameter is not followed: if it were, the
	// stack-status value passed here would mark every other EqualFold call.
	var stack models.Stack
	_ = strings.EqualFold(stack.Status, label)
	_ = strings.EqualFold(label, "running")
}

// DIRECTIVES.
func ignored(stack *models.Stack) {
	//wirevalue:ignore the fixture models a legacy row the migration rewrites
	stack.Status = "stopped"
	stack.Status = "running" //wirevalue:ignore trailing form covers its own line
	stack.Status = "partial" // want `bare "partial" is assigned`
}

func badDirectives(stack *models.Stack) {
	/* want `wirevalue:ignore needs a reason` */ //wirevalue:ignore
	stack.Status = "stopped"                     // want `bare "stopped" is assigned`
	/* want `wirevalue:ignore must have no space after //` */ // wirevalue:ignore spaced so it is an ordinary comment
	stack.Status = "running"                                  // want `bare "running" is assigned`
}
