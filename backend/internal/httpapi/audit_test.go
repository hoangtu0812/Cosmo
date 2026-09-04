package httpapi

import (
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// The filter clause is assembled with placeholder numbers rather than values,
// which is exactly the kind of thing that compiles and then binds the wrong
// argument. These check the numbering, and that a value never lands in the SQL.
func TestAuditFilterWhere(t *testing.T) {
	empty := auditFilter{}
	clause, args := empty.where()
	if clause != "TRUE" || len(args) != 0 {
		t.Fatalf("empty filter should match everything, got %q with %d args", clause, len(args))
	}

	from := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	filter := auditFilter{Action: "tool.created", WorkspaceID: "ws_1", From: &from, Before: 42, Search: "tu@example.com"}
	clause, args = filter.where()
	if len(args) != 5 {
		t.Fatalf("expected 5 bound arguments, got %d: %v", len(args), args)
	}
	for index, want := range []string{"$1", "$2", "$3", "$4", "$5"} {
		if !strings.Contains(clause, want) {
			t.Fatalf("clause is missing placeholder %s for argument %d: %s", want, index+1, clause)
		}
	}
	// Every value belongs in args. A filter value appearing in the SQL text
	// would mean the search box is a way to write queries.
	for _, value := range []string{"tool.created", "ws_1", "tu@example.com"} {
		if strings.Contains(clause, value) {
			t.Fatalf("filter value %q was interpolated into the clause: %s", value, clause)
		}
	}
	// The search box binds once and is compared against several columns.
	if strings.Count(clause, "$5") != 6 {
		t.Fatalf("search should reuse one placeholder across every searched column: %s", clause)
	}
}

func TestAuditFilterFrom(t *testing.T) {
	request := httptest.NewRequest("GET", "/api/admin/audit-logs?limit=500&action=tool.created&before=7&from=2026-01-01T00:00:00Z", nil)
	filter := auditFilterFrom(request, 50, 200)
	// A limit past the maximum falls back to the default rather than being
	// clamped up to it: a caller asking for 500 is guessing, not tuning.
	if filter.Limit != 50 {
		t.Fatalf("limit above the maximum should fall back to the default, got %d", filter.Limit)
	}
	if filter.Action != "tool.created" || filter.Before != 7 || filter.From == nil {
		t.Fatalf("filter did not read the query: %+v", filter)
	}
	if filter.To != nil {
		t.Fatalf("an absent bound should stay absent, got %v", filter.To)
	}
}

func TestClientIP(t *testing.T) {
	request := httptest.NewRequest("GET", "/", nil)
	request.RemoteAddr = "10.1.2.3:54321"
	if got := clientIP(request); got != "10.1.2.3" {
		t.Fatalf("expected the address without its port, got %q", got)
	}
	// A proxy that hands over a bare address rather than host:port is common
	// enough that dropping the value would lose the field entirely.
	request.RemoteAddr = "10.1.2.3"
	if got := clientIP(request); got != "10.1.2.3" {
		t.Fatalf("expected a bare address to survive, got %q", got)
	}
}
