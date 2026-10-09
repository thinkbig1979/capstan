// Package database (directory dbpkg: "database" is a standard-library path)
// carries the real package name so the append arm, which is
// scoped to package database, runs here.
package database

import (
	"context"
	"database/sql"
	"time"
)

type myTime time.Time

type nullish = sql.NullTime

func binds(ctx context.Context, db *sql.DB, tx *sql.Tx, conn *sql.Conn, stmt *sql.Stmt, t time.Time) {
	db.Exec("q", "id", t)                 // want `time.Time bound as an SQL argument`
	db.Exec("q", &t)                      // want `\*time.Time bound as an SQL argument`
	db.ExecContext(ctx, "q", t)           // want `time.Time bound as an SQL argument`
	db.Query("q", t)                      // want `time.Time bound as an SQL argument`
	db.QueryContext(ctx, "q", t)          // want `time.Time bound as an SQL argument`
	db.QueryRow("q", t)                   // want `time.Time bound as an SQL argument`
	db.QueryRowContext(ctx, "q", t)       // want `time.Time bound as an SQL argument`
	tx.Exec("q", time.Now())              // want `time.Time bound as an SQL argument`
	conn.ExecContext(ctx, "q", myTime(t)) // want `myTime bound as an SQL argument`
	stmt.Exec(sql.NullTime{Time: t})      // want `sql.NullTime bound as an SQL argument`
	stmt.Query(nullish{})                 // want `nullish bound as an SQL argument`
	stmt.QueryRow(sql.Null[time.Time]{})  // want `sql.Null\[time.Time\] bound as an SQL argument`
}

type holder struct{ *sql.DB }

// promoted: Exec reached through an embedded *sql.DB is still sql's Exec.
func promoted(h holder, t time.Time) {
	h.Exec("q", t) // want `time.Time bound as an SQL argument`
}

func appended(db *sql.DB, t time.Time) {
	var args []interface{}
	args = append(args, "x", t) // want `time.Time appended to an SQL argument list`
	var more []any
	more = append(more, &t) // want `\*time.Time appended to an SQL argument list`
	db.Query("q", append(args, more...)...)
}
