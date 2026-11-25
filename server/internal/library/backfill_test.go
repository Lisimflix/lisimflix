package library

import (
	"strings"
	"testing"
)

// #238 review: serve runs BackfillVersions on every start; once every version
// is numbered, finding none left must read the (then empty) partial index,
// not all of library_versions.
func TestBackfillQueryUsesAnIndex(t *testing.T) {
	s, _ := newStore(t)
	var plan string
	rows, err := s.db.Query(`EXPLAIN QUERY PLAN `+unnumberedQuery, backfillBatch)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	for rows.Next() {
		var id, parent, unused int
		var detail string
		if err := rows.Scan(&id, &parent, &unused, &detail); err != nil {
			t.Fatal(err)
		}
		plan += detail + "\n"
	}
	if !strings.Contains(plan, "library_versions_unnumbered") {
		t.Fatalf("plan:\n%s", plan)
	}
}
