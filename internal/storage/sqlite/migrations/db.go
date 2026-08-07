package migrations

import "database/sql"

// DB is the database handle a migration runs against.
//
// It is an interface rather than *sql.DB so the whole migration pass can run on
// the single connection that holds the EXCLUSIVE transaction. On a *sql.DB the
// pool is free to hand each statement a different connection, which would
// autocommit a ledger row outside the transaction its migration rolls back in,
// skipping that migration forever (bd-ok4pr.1.9).
//
// *sql.DB, *sql.Tx and the connection wrapper in the sqlite package all satisfy
// it.
type DB interface {
	Exec(query string, args ...any) (sql.Result, error)
	Query(query string, args ...any) (*sql.Rows, error)
	QueryRow(query string, args ...any) *sql.Row
	Prepare(query string) (*sql.Stmt, error)
}
