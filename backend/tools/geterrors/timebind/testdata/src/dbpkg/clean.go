package database

import (
	"database/sql"
	"fmt"
	"time"
)

const layout = "2006-01-02T15:04:05.000Z"

func storedInstant(t time.Time) string { return t.UTC().Format(layout) }

// notSQL has the same method names as *sql.DB; only database/sql's are binds.
type notSQL struct{}

func (notSQL) Exec(string, ...any) {}

func clean(db *sql.DB, n notSQL, t time.Time) {
	db.Exec("q", storedInstant(t))
	db.Exec("q", t.Unix())
	db.Exec("q", t.Format(time.RFC3339))
	db.Exec("q", sql.NullString{})
	n.Exec("q", t)
	var times []time.Time
	times = append(times, t)
	var strs []string
	strs = append(strs, storedInstant(t))
	fmt.Println(t, times, strs)
	var dest time.Time
	_ = db.QueryRow("q").Scan(&dest) // a Scan destination is a read, not a bind
}
