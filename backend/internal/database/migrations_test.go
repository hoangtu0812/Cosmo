package database

import "testing"

func TestMigrationsAreOrderedAndNamed(t *testing.T) {
	if err := validateMigrations(migrations); err != nil {
		t.Fatal(err)
	}
}

func TestMigrationChecksumChangesWithStatements(t *testing.T) {
	first := Migration{Version: 1, Name: "one", Statements: []string{"SELECT 1"}}
	second := Migration{Version: 1, Name: "one", Statements: []string{"SELECT 2"}}
	if migrationChecksum(first) == migrationChecksum(second) {
		t.Fatal("different migration statements produced the same checksum")
	}
}

func TestMigrationValidationRejectsDuplicateVersion(t *testing.T) {
	err := validateMigrations([]Migration{
		{Version: 1, Name: "one", Statements: []string{"SELECT 1"}},
		{Version: 1, Name: "two", Statements: []string{"SELECT 2"}},
	})
	if err == nil {
		t.Fatal("expected duplicate migration version to fail")
	}
}
