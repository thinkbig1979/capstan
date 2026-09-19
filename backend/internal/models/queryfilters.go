package models

import "time"

// The filter structs below are query-parameter shapes for internal handler use,
// not wire types: they carry no json tags at all and never reach a response
// body. They live in their own file so backend/tygo.yaml can exclude the whole
// file from TypeScript generation — tygo v0.2.21 excludes by file, not by type
// name — which keeps tygo from emitting Go field names (Page, Limit, From) as
// if they were a JSON contract.

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

// BackupHistoryFilters is the query shape for GET /backups/history, mirroring
// UpdateHistoryFilters above. Kind replaces the update log's container/stack
// dimensions: backup_runs has no stack column (see the CREATE TABLE in
// database/migrations.go), so there is no per-stack filter to offer.
type BackupHistoryFilters struct {
	Page    int
	Limit   int
	Status  string
	Kind    string
	Trigger string
	From    *time.Time
	To      *time.Time
}
