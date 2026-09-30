package task

// Column ↔ scan-slot drift guard.
//
// Why this exists: every work-item column (type/owner/due/parent_id/…) was
// added by ALTER TABLE and then appended to `taskColumns` and to `scanTask`.
// A SELECT that returns 26 columns while scanTask passes 25 destinations does
// not fail at compile time and does not fail at build time — it fails on the
// first real query against a real database, at runtime, as a scan error.
//
// That is exactly the failure we could not check while working without a
// Postgres instance (docs/学习muse/evidence: "改错就是运行时扫描错位").
// This test is the part of that risk that CAN be checked with no database:
// it counts the columns and counts the destinations, by actually invoking
// scanTask against a stub row.
//
// It catches: a column added to taskColumns without a matching destination,
// a destination added without a column, and a duplicate column name.

import (
	"reflect"
	"strconv"
	"strings"
	"testing"
)

// recordingRow stands in for pgx.Row. It records the destination pointers and
// leaves every value at its zero value, which is enough to count slots.
type recordingRow struct {
	dests []any
}

func (r *recordingRow) Scan(dest ...any) error {
	r.dests = dest
	return nil
}

// splitColumns splits a SELECT list on top-level commas, ignoring commas
// inside parentheses or quotes. `COALESCE(workstream_id, ”)` contains a comma
// that must not split the list.
func splitColumns(list string) []string {
	var out []string
	depth := 0
	inQuote := false
	start := 0
	for i, r := range list {
		switch {
		case r == '\'':
			inQuote = !inQuote
		case inQuote:
			// nothing to do
		case r == '(':
			depth++
		case r == ')':
			depth--
		case r == ',' && depth == 0:
			out = append(out, strings.TrimSpace(list[start:i]))
			start = i + 1
		}
	}
	if tail := strings.TrimSpace(list[start:]); tail != "" {
		out = append(out, tail)
	}
	return out
}

func TestTaskColumnsMatchScanDestinations(t *testing.T) {
	row := &recordingRow{}
	if _, err := scanTask(row); err != nil {
		t.Fatalf("scanTask with a stub row returned an error: %v", err)
	}
	cols := splitColumns(taskColumns)
	if len(cols) != len(row.dests) {
		t.Fatalf("taskColumns selects %d columns but scanTask passes %d destinations:\n  %s",
			len(cols), len(row.dests), strings.Join(cols, ", "))
	}
}

// Every destination must be a pointer. Passing a value would compile fine and
// silently drop the column on the floor. Nullable columns are scanned through
// a pointer-to-pointer (`var x *int64; … &x`), which is why this checks the
// kind rather than a list of concrete types.
func TestScanTaskDestinationsArePointers(t *testing.T) {
	row := &recordingRow{}
	if _, err := scanTask(row); err != nil {
		t.Fatalf("scanTask with a stub row returned an error: %v", err)
	}
	for i, d := range row.dests {
		if d == nil {
			t.Errorf("destination %d is nil", i)
			continue
		}
		if reflect.ValueOf(d).Kind() != reflect.Ptr {
			t.Errorf("destination %d has type %T, which pgx cannot scan into", i, d)
		}
	}
}

// A duplicated column in the SELECT list would shift every later destination
// by one while keeping the count correct — the one drift shape the count check
// above cannot see.
func TestTaskColumnsHaveNoDuplicates(t *testing.T) {
	seen := map[string]bool{}
	for _, c := range splitColumns(taskColumns) {
		if seen[c] {
			t.Errorf("taskColumns lists %q twice", c)
		}
		seen[c] = true
	}
}

// The work-item columns P0 appended. If one is dropped from the SELECT list
// the model silently renders blanks in the UI — the exact failure the old
// unchecked `category` field caused.
func TestTaskColumnsIncludeWorkItemColumns(t *testing.T) {
	seen := map[string]bool{}
	for _, c := range splitColumns(taskColumns) {
		seen[c] = true
	}
	required := []string{
		"type", "owner_id", "assignees", "due_at", "remind_at", "parent_id",
		"origin_kind", "origin_ref", "tags", "visibility",
	}
	for _, r := range required {
		if !seen[r] {
			t.Errorf("taskColumns is missing the work-item column %q; the UI would render it blank", r)
		}
	}
}

// --- INSERT shape ---

// An INSERT whose column list and placeholder list disagree does not fail at
// compile time or at build time; it fails on the first real insert against a
// real database. This is the write-side counterpart of the scan check above.
func TestTaskInsertColumnsMatchPlaceholders(t *testing.T) {
	cols := len(splitColumns(taskInsertColumns))
	plain := countPlaceholders(taskInsertValues)
	upsert := countPlaceholders(taskUpsertValues)

	if plain != cols {
		t.Errorf("taskInsertColumns has %d columns but taskInsertValues has %d placeholders", cols, plain)
	}
	if upsert != cols {
		t.Errorf("taskInsertColumns has %d columns but taskUpsertValues has %d placeholders", cols, upsert)
	}
}

// Placeholders must be numbered 1..N with no gaps or repeats: a duplicated
// $5 silently overwrites an argument.
func TestTaskInsertPlaceholdersAreSequential(t *testing.T) {
	for name, values := range map[string]string{
		"taskInsertValues": taskInsertValues,
		"taskUpsertValues": taskUpsertValues,
	} {
		seen := map[int]bool{}
		for _, n := range placeholderNumbers(values) {
			if seen[n] {
				t.Errorf("%s uses $%d twice", name, n)
			}
			seen[n] = true
		}
		total := len(seen)
		for i := 1; i <= total; i++ {
			if !seen[i] {
				t.Errorf("%s is missing $%d (placeholders must be 1..%d with no gaps)", name, i, total)
			}
		}
	}
}

// The two INSERT statements must select the same columns. They differ only in
// the NULLIF wrapping of the upsert values, so a column added to one and not
// the other means an upsert silently drops a field.
func TestTaskInsertAndUpsertSelectTheSameColumns(t *testing.T) {
	// Both statements now share taskInsertColumns, so this is a guard against
	// someone re-inlining one of them and drifting.
	if !strings.Contains(taskInsertValues, "$22") || !strings.Contains(taskUpsertValues, "$22") {
		t.Error("the highest placeholder moved; re-check the shared column list")
	}
}

// countPlaceholders counts distinct $N references in a VALUES clause.
func countPlaceholders(values string) int {
	seen := map[int]bool{}
	for _, n := range placeholderNumbers(values) {
		seen[n] = true
	}
	return len(seen)
}

// placeholderNumbers extracts the numbers from $1, $2 … references.
func placeholderNumbers(s string) []int {
	var out []int
	for i := 0; i < len(s); i++ {
		if s[i] != '$' {
			continue
		}
		j := i + 1
		for j < len(s) && s[j] >= '0' && s[j] <= '9' {
			j++
		}
		if j == i+1 {
			continue
		}
		n, err := strconv.Atoi(s[i+1 : j])
		if err != nil {
			continue
		}
		out = append(out, n)
		i = j - 1
	}
	return out
}
