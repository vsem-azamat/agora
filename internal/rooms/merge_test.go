package rooms

import (
	"context"
	"regexp"
	"slices"
	"testing"

	"github.com/vsem-azamat/agora/internal/store"
)

// notMerged are the references to messages that mergeEcho leaves alone, and why: none of them
// can point at an echo when the answer merges it.
var notMerged = map[string]string{
	"messages.echo_of":  "it points only at a message still sending, which an echo, come in from outside, never is",
	"messages.sent_out": "a note points at the message it sent out, which was posted in Agora, never one come in from outside",
}

// TestTheEchoMergeCoversEveryReferenceToAMessage fails when a column that references messages
// is neither moved nor deleted by mergeEcho (nor listed in notMerged): deleting the echo would then
// fail on the foreign key, and the answer of the bridge with it.
func TestTheEchoMergeCoversEveryReferenceToAMessage(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	db, err := store.Open(ctx, ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	rows, err := db.QueryContext(ctx, `SELECT t.name, k."from" FROM sqlite_schema AS t, pragma_foreign_key_list(t.name) AS k
		WHERE t.type = 'table' AND k."table" = 'messages' ORDER BY 1, 2`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var refs []string
	for rows.Next() {
		var table, column string
		if err := rows.Scan(&table, &column); err != nil {
			t.Fatal(err)
		}
		refs = append(refs, table+"."+column)
		if notMerged[table+"."+column] != "" {
			continue
		}
		// an UPDATE that moves the column off the echo, or a DELETE of the rows that hold it
		handles := regexp.MustCompile(`(?is)^\s*(?:UPDATE(?:\s+OR\s+\w+)?\s+` + table + `\s+SET\s+` + column + `\s*=.*\bWHERE\s+` + column +
			`\s*=\s*:echo|DELETE\s+FROM\s+` + table + `\s+WHERE\s+` + column + `\s*=\s*:echo)`)
		if !slices.ContainsFunc(mergeEcho, handles.MatchString) {
			t.Errorf("merging an echo leaves %s.%s pointing at it", table, column)
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(refs, "messages.reply_to") {
		t.Fatalf("found references %v: the scan is broken", refs)
	}
	for ref := range notMerged {
		if !slices.Contains(refs, ref) {
			t.Errorf("notMerged lists %s, which no longer references messages", ref)
		}
	}
	// the echo row itself goes last
	if !regexp.MustCompile(`(?i)^DELETE FROM messages WHERE id = :echo$`).MatchString(mergeEcho[len(mergeEcho)-1]) {
		t.Errorf("mergeEcho does not end by deleting the echo: %q", mergeEcho[len(mergeEcho)-1])
	}
}
