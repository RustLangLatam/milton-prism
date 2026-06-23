package agent_test

import (
	"strings"
	"testing"

	"milton_prism/core/worker/generation/infrastructure/agent"
)

// TestStoreSection_MongoDBInjectsNothing pins the non-regression contract: the
// MongoDB store (the original path) and an empty store inject NO block, so the
// established Mongo prompt behaviour is unchanged for every profile.
func TestStoreSection_MongoDBInjectsNothing(t *testing.T) {
	for _, store := range []string{"", "mongodb", "MongoDB"} {
		for _, profile := range []string{"go", "python", "node", "rust"} {
			if got := agent.StoreSection(profile, store); got != "" {
				t.Errorf("StoreSection(%q, %q) = %q, want empty", profile, store, got)
			}
		}
	}
}

// TestStoreSection_GoPostgres asserts the Go + PostgreSQL block instructs the
// generator to emit the raw-SQL persistence layer: pgx, no ORM, postgres repos,
// a pool client, golang-migrate migrations, sequence IDs, and DATABASE_URL/DB_*
// env with zero MONGO_* — the v1 certified SQL cell.
func TestStoreSection_GoPostgres(t *testing.T) {
	s := agent.StoreSection("go", "postgres")
	if s == "" {
		t.Fatal("StoreSection(go, postgres) is empty, want a PostgreSQL block")
	}
	for _, want := range []string{
		"PostgreSQL",
		"pgx",
		"no ORM",
		"postgres_<resource>_repository.go",
		"postgres_client",
		"golang-migrate",
		"migrations/",
		"WithTransaction",
		"ON CONFLICT",
		"DATABASE_URL",
		"DB_HOST",
		"go build ./...",
	} {
		if !strings.Contains(s, want) {
			t.Errorf("Go+Postgres store section missing %q", want)
		}
	}
	// The block names MONGO_* only to FORBID it ("Do NOT emit any MONGO_* variable").
	// It must not instruct the generator to SET one (no `MONGO_…=` assignment).
	if strings.Contains(s, "MONGO_URI") || strings.Contains(s, "MONGO_DATABASE") {
		t.Errorf("Go+Postgres store section must not prescribe MONGO_* env vars")
	}
}

// TestStoreSection_SQLHoleIsHonest asserts that a SQL store on a non-Go profile
// (a v1 hole) yields an honest "NOT generated in v1" note that forbids guessing,
// rather than a fabricated implementation. (Unreachable while the
// IsGenerableDatabase guard rejects the cell at creation, but kept self-consistent.)
func TestStoreSection_SQLHoleIsHonest(t *testing.T) {
	s := agent.StoreSection("python", "postgres")
	if s == "" {
		t.Fatal("StoreSection(python, postgres) is empty, want an honest hole note")
	}
	for _, want := range []string{"NOT generated in v1", "Do NOT guess", "MongoDB"} {
		if !strings.Contains(s, want) {
			t.Errorf("SQL-hole store section missing %q; got:\n%s", want, s)
		}
	}
}
