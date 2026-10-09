package skiptest

import (
	"database/sql"
	"time"
)

func fixture(db *sql.DB, t time.Time) {
	db.Exec("q", t)
}
