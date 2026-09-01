# Database migrations

Cosmo records schema versions in `schema_migrations`. Every migration has an
immutable SHA-256 checksum and runs inside one transaction while a PostgreSQL
advisory lock prevents multiple replicas from migrating concurrently.

## Commands

From the backend source directory:

```powershell
go run ./cmd/migrate status
go run ./cmd/migrate up
```

From Docker Compose:

```powershell
docker compose exec backend cosmo-migrate status
docker compose exec backend cosmo-migrate up
```

The API also applies pending migrations during startup. The explicit command is
provided for deployment pipelines that require schema changes to finish before
new application replicas start.

## Adding a migration

1. Append a new `Migration` entry in `internal/database/migrations.go`.
2. Use a new monotonically increasing version.
3. Never edit a migration that has been applied in any shared environment.
4. Prefer additive, backward-compatible changes.
5. Add data backfills as bounded statements or a separate operational job when
   the table is too large for one transaction.
6. Run backend tests and rehearse against a restored copy of the current
   database before deployment.

If an applied checksum differs from the binary, startup fails closed. Create a
new forward-fix migration instead of modifying the old statement.

## Recovery

- A failed migration transaction is rolled back and is not recorded.
- Fix the cause, create a forward-fix when necessary, then run `up` again.
- Do not manually insert or update `schema_migrations` rows.
- Back up PostgreSQL before destructive or long-running migrations.
