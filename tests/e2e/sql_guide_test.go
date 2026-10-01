//go:build slow

// Copyright 2026 The Gravix Authors
// SPDX-License-Identifier: Apache-2.0

package e2e

import (
	"database/sql"
	"html"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	_ "github.com/trinodb/trino-go-client/trino"
)

// TestSQLGuideQueriesRunOnTrino runs every query on the SQL-for-PromQL-users
// page against the live stack's Trino.
//
// GRVX-1103 shipped those queries reasoned from the column types and never
// executed (SD-052). The first time they ran, two of them reported a rate of
// 0.0 for ten requests in five minutes: SUM(request_count) / 300.0 is BIGINT
// over DECIMAL(4,1), and Trino keeps one decimal place (CD-006). A governance
// test now rejects the pattern in the text. This one asks Trino, so it also
// catches whatever the text check does not anticipate.
func TestSQLGuideQueriesRunOnTrino(t *testing.T) {
	requireLiveStack(t)

	raw, err := os.ReadFile(filepath.Join(repoRootFromE2E(t), "docs-site", "docs", "sql-vs-promql.md"))
	if err != nil {
		t.Fatal(err)
	}
	var queries []string
	for _, m := range regexp.MustCompile(`(?s)<pre>(.*?)</pre>`).FindAllStringSubmatch(string(raw), -1) {
		queries = append(queries, html.UnescapeString(strings.ReplaceAll(m[1], "<br/>", "\n")))
	}
	if len(queries) < 5 {
		t.Fatalf("found %d queries on the page, expected 5; if the table changed shape, move this test with it", len(queries))
	}

	db, err := sql.Open("trino", hiveDSN)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	ran := 0
	for i, q := range queries {
		// The raw-facts table is not created by the full stack, because raw
		// facts sit under per-tenant paths no Hive table can name (F-070).
		// When it exists, its query runs like the others.
		if strings.Contains(q, "request_facts") {
			var n int
			if err := db.QueryRow("SELECT count(*) FROM information_schema.tables " +
				"WHERE table_schema = 'raw' AND table_name = 'request_facts'").Scan(&n); err != nil {
				t.Fatalf("checking for request_facts: %v", err)
			}
			if n == 0 {
				t.Logf("query %d not run: gravix.raw.request_facts does not exist on this stack (F-070)", i+1)
				continue
			}
		}

		rows, err := db.Query(q)
		if err != nil {
			t.Errorf("query %d does not run on Trino: %v\n%s", i+1, err, q)
			continue
		}
		types, err := rows.ColumnTypes()
		if err != nil {
			rows.Close()
			t.Fatalf("query %d: reading its column types: %v", i+1, err)
		}
		for _, c := range types {
			if (c.Name() == "rps" || c.Name() == "error_ratio") && !strings.EqualFold(c.DatabaseTypeName(), "double") {
				t.Errorf("query %d returns %s as %s; a rate or ratio must be a double, or small "+
					"values round to zero (CD-006)\n%s", i+1, c.Name(), c.DatabaseTypeName(), q)
			}
		}
		n := 0
		for rows.Next() {
			n++
		}
		if err := rows.Err(); err != nil {
			t.Errorf("query %d failed while reading its rows: %v\n%s", i+1, err, q)
		}
		rows.Close()
		t.Logf("query %d ran and returned %d row(s)", i+1, n)
		ran++
	}
	if ran < 4 {
		t.Errorf("only %d of the page's queries ran; the four metric queries must", ran)
	}
}
