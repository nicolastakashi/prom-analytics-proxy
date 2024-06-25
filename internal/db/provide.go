package db

import "database/sql"

type Provider interface {
	WithDB(func(db *sql.DB))
}
