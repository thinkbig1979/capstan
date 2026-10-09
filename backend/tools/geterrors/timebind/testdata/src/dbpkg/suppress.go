package database

import (
	"database/sql"
	"time"
)

// The malformed cases put the expectation in a block comment on purpose: a
// trailing line comment would become the directive's reason and suppress
// the finding it asserts.

func trailing(db *sql.DB, t time.Time) {
	db.Exec("q", t) //timebind:ignore read back only via Scan
}

func above(db *sql.DB, t time.Time) {
	//timebind:ignore read back only via Scan
	db.Exec("q", t)
}

func twoAbove(db *sql.DB, t time.Time) {
	//timebind:ignore too far away to apply

	db.Exec("q", t) // want `time.Time bound as an SQL argument`
}

func trailingDoesNotReachNextLine(db *sql.DB, t time.Time) {
	db.Exec("q", t) //timebind:ignore read back only via Scan
	db.Exec("q", t) // want `time.Time bound as an SQL argument`
}

func noReason(db *sql.DB, t time.Time) {
	db.Exec("q", t) /* want "needs a reason" "time.Time bound as an SQL argument" */ //timebind:ignore
}

func spaced(db *sql.DB, t time.Time) {
	db.Exec("q", t) /* want "no space after" "time.Time bound as an SQL argument" */ // timebind:ignore read back only via Scan
}

func glued(db *sql.DB, t time.Time) {
	db.Exec("q", t) /* want "time.Time bound as an SQL argument" */ //timebind:ignorereason glued to the name
}
