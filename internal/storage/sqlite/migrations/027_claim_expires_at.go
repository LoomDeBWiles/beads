package migrations

import (
	"fmt"
)

// MigrateClaimExpiresAtColumn adds the claim_expires_at column to the issues table.
// The column stores the expiry of an owner lease taken by `bd claim`; NULL means the
// claim never expires, which preserves the pre-lease behaviour of human claims (bd-ok4pr).
func MigrateClaimExpiresAtColumn(db DB) error {
	// Check if column already exists
	var columnExists bool
	err := db.QueryRow(`
		SELECT COUNT(*) > 0
		FROM pragma_table_info('issues')
		WHERE name = 'claim_expires_at'
	`).Scan(&columnExists)
	if err != nil {
		return fmt.Errorf("failed to check claim_expires_at column: %w", err)
	}

	if columnExists {
		return nil
	}

	// Add the claim_expires_at column (nullable: NULL = lease never expires)
	_, err = db.Exec(`ALTER TABLE issues ADD COLUMN claim_expires_at TIMESTAMP`)
	if err != nil {
		return fmt.Errorf("failed to add claim_expires_at column: %w", err)
	}

	return nil
}
