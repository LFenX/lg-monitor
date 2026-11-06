package config

import (
	"strings"
	"testing"
	"time"
)

func TestRuleSQLRejectsSystemClockAndQualifiedNamesWithoutMisreadingStrings(t *testing.T) {
	for _, query := range []string{`SELECT CURRENT_TIMESTAMP AS key`, `SELECT CURRENT_DATE AS key`, `SELECT CURRENT_TIME AS key`, `SELECT datetime('NOW') AS key`, `SELECT date() AS key`, `SELECT time( /* no time */ ) AS key`, `SELECT datetime() AS key`, `SELECT julianday() AS key`, `SELECT unixepoch() AS key`, `SELECT strftime('%s') AS key`, `SELECT * FROM main.target_status`, `SELECT * FROM "main".target_status`, `SELECT * FROM [temp].target_status`, "SELECT * FROM `main`.target_status", `WITH target_status AS (SELECT * FROM main.target_status) SELECT * FROM target_status`} {
		if err := ValidateRuleSQL(query); err == nil {
			t.Fatalf("forbidden SQL accepted: %s", query)
		}
	}
	for _, query := range []string{`SELECT datetime(ts/1000,'unixepoch') AS key FROM target_status`, `SELECT strftime('%Y',ts/1000,'unixepoch') AS key FROM target_status`, `SELECT 'main.target_status CURRENT_TIMESTAMP date() now in ordinary content' AS key`, `SELECT :now AS key -- CURRENT_TIMESTAMP main.target_status date()`, `SELECT 'snow' AS key /* datetime('now') */`} {
		if err := ValidateRuleSQL(query); err != nil {
			t.Fatalf("ordinary content or explicit timestamp rejected: %s: %v", query, err)
		}
	}
	if interval, err := (Rule{Type: "sql"}).SQLInterval(); err != nil || interval != time.Minute {
		t.Fatalf("SQL default interval: %v %v", interval, err)
	}
	if _, err := (Rule{Type: "sql", Interval: "9s"}).SQLInterval(); err == nil || !strings.Contains(err.Error(), "10s") {
		t.Fatalf("SQL minimum interval not enforced: %v", err)
	}
}
