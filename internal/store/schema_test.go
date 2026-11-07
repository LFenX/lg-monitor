package store

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"testing/fstest"
)

func TestSchemaExplainsEveryActualTableAndColumnFromMigrationComments(t *testing.T) {
	ctx := context.Background()
	s := openStore(t, Options{DisableMaintenance: true})
	view, err := OpenView(ctx, s.maintenanceView.path)
	if err != nil {
		t.Fatal(err)
	}
	defer view.Close()
	schema, err := view.Schema(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var actual int
	if err := s.db.QueryRowContext(ctx, `SELECT count(*) FROM sqlite_schema WHERE type IN ('table','view')`).Scan(&actual); err != nil {
		t.Fatal(err)
	}
	if len(schema.Tables) != actual || len(schema.Tables) != 30 {
		t.Fatalf("schema missed objects: described=%d actual=%d", len(schema.Tables), actual)
	}
	columns := 0
	for _, table := range schema.Tables {
		if table.Description == "" || len(table.Columns) == 0 {
			t.Errorf("undocumented table: %+v", table)
		}
		if table.Name == "api_tokens" || table.Name == "collector_inbox_single" {
			t.Fatalf("private or retired table exposed: %s", table.Name)
		}
		for _, column := range table.Columns {
			columns++
			if column.Description == "" || column.Type == "" {
				t.Errorf("undocumented column: %s.%s", table.Name, column.Name)
			}
			if table.Name == "collector_inbox" && column.Name == "skipped" && (column.Default == nil || *column.Default != "0" || !column.NotNull) {
				t.Fatalf("actual default/nullability not reported: %+v", column)
			}
			if table.Name == "collector_offsets" && column.Name == "collector_id" && column.PrimaryKeyPosition != 1 {
				t.Fatal("primary key position was not reported")
			}
		}
	}
	if columns < 180 {
		t.Fatalf("schema column inventory is incomplete: %d", columns)
	}
	if data, err := json.Marshal(schema); err != nil || !strings.Contains(string(data), `"default":null`) {
		t.Fatalf("nullable schema JSON: %s %v", data, err)
	}
	schema.Tables[0].Columns[0].Description = "mutated"
	again, err := view.Schema(ctx)
	if err != nil || again.Tables[0].Columns[0].Description == "mutated" {
		t.Fatal("schema response mutated its source")
	}
}

func TestMigrationDescriptionParserUsesOrderedCommentsAndReportsBadEntries(t *testing.T) {
	files := fstest.MapFS{
		"migrations/001_first.sql":  {Data: []byte("-- ordinary comment\n-- @table demo Initial table.\n-- @column demo.id,demo.other Shared identity.\n")},
		"migrations/002_second.sql": {Data: []byte("-- @table demo Updated table.\n")},
	}
	docs, err := migrationDocs(files)
	if err != nil || docs.tables["demo"] != "Updated table." || docs.columns["demo.other"] != "Shared identity." {
		t.Fatalf("migration comments were not parsed: %+v %v", docs, err)
	}
	for _, text := range []string{"-- @table demo", "-- @column demo Missing column.", "-- @table bad.name Wrong object.", "-- @column demo.bad-name Wrong identifier."} {
		_, err := migrationDocs(fstest.MapFS{"migrations/001_bad.sql": {Data: []byte(text)}})
		if err == nil || !strings.Contains(err.Error(), "第 1 行") {
			t.Fatalf("bad documentation not located: %q %v", text, err)
		}
	}
}
