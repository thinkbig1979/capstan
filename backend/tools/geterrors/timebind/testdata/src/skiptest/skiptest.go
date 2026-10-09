// Package skiptest: the production file fires, the _test.go file is skipped.
package skiptest

import (
	"database/sql"
	"time"
)

func prod(db *sql.DB, t time.Time) {
	db.Exec("q", t) // want `time.Time bound as an SQL argument`
}
