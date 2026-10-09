// Package other is not package database: the bind arm still runs, the append
// arm does not (a []any outside package database is mostly logging).
package other

import (
	"database/sql"
	"time"
)

func other(db *sql.DB, t time.Time) {
	db.Exec("q", t) // want `time.Time bound as an SQL argument`
	var fields []any
	fields = append(fields, "at", t)
	_ = fields
}
