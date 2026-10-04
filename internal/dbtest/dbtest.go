package dbtest

import (
	"database/sql"

	"9router/proxy/internal/db"
)

// CreateTables builds the schema a test fixture needs. It delegates to the
// production bootstrap so a fixture has the same shape as a real database —
// a second copy of the DDL here is how tests kept passing against tables the
// server never creates (settings was missing until EnsureCoreSchema landed).
func CreateTables(database *sql.DB) error {
	return db.EnsureCoreSchema(database)
}
