package database

// Seed from the old hash input so currently valid policies remain valid.
// Already-stale policies are not silently reauthorized by this migration.
var toolAuthPolicyRevisionStatements = []string{
	`ALTER TABLE tools ADD COLUMN auth_updated_at TIMESTAMPTZ`,
	`UPDATE tools SET auth_updated_at=updated_at`,
	`ALTER TABLE tools ALTER COLUMN auth_updated_at SET DEFAULT NOW(), ALTER COLUMN auth_updated_at SET NOT NULL`,
	`CREATE FUNCTION track_tool_auth_revision() RETURNS trigger LANGUAGE plpgsql AS $$
	BEGIN
		IF NEW.auth_secret IS DISTINCT FROM OLD.auth_secret
		   OR NEW.auth_type IS DISTINCT FROM OLD.auth_type
		   OR NEW.auth_header_name IS DISTINCT FROM OLD.auth_header_name THEN
			NEW.auth_updated_at := clock_timestamp();
		ELSE
			NEW.auth_updated_at := OLD.auth_updated_at;
		END IF;
		RETURN NEW;
	END; $$`,
	`CREATE TRIGGER tool_auth_revision BEFORE UPDATE ON tools FOR EACH ROW EXECUTE FUNCTION track_tool_auth_revision()`,
}
