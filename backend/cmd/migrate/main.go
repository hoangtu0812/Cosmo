package main

import (
	"context"
	"fmt"
	"os"

	"cosmo/backend/internal/database"

	"github.com/jackc/pgx/v5/pgxpool"
)

func main() {
	databaseURL := os.Getenv("DATABASE_URL")
	if databaseURL == "" {
		databaseURL = "postgres://cosmo:cosmo@localhost:5432/cosmo?sslmode=disable"
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		fatal(err)
	}
	defer pool.Close()
	command := "up"
	if len(os.Args) > 1 {
		command = os.Args[1]
	}
	switch command {
	case "up":
		if err := database.Migrate(ctx, pool); err != nil {
			fatal(err)
		}
		fmt.Println("database migrations applied")
	case "status":
		statuses, err := database.Status(ctx, pool)
		if err != nil {
			fatal(err)
		}
		for _, status := range statuses {
			state := "pending"
			if status.AppliedAt != nil {
				state = "applied " + status.AppliedAt.Format("2006-01-02T15:04:05Z07:00")
			}
			fmt.Printf("%03d %-28s %s\n", status.Version, status.Name, state)
		}
	default:
		fatal(fmt.Errorf("unknown command %q; use up or status", command))
	}
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, err)
	os.Exit(1)
}
