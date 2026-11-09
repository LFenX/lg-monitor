//go:build windows && integration

package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/LFenX/lg-monitor/internal/store"
)

func TestM1AcceptanceCLIProcess(t *testing.T) {
	if os.Getenv("LGMON_TEST_ACCEPTANCE_CLI") != "1" {
		return
	}
	for i, arg := range os.Args {
		if arg == "--" {
			command := newRootCommand()
			command.SetArgs(os.Args[i+1:])
			if err := command.Execute(); err != nil {
				fmt.Fprintln(os.Stderr, err)
				os.Exit(1)
			}
			os.Exit(0)
		}
	}
	os.Exit(2)
}

func TestNativeM1AcceptanceReadsRealDatabaseWithoutChangingIt(t *testing.T) {
	ctx := context.Background()
	const at int64 = 2000000000000
	dir := filepath.Join(t.TempDir(), "验收's $() [scope]")
	home := filepath.Join(dir, "monitor")
	if err := os.MkdirAll(filepath.Join(home, "config"), 0700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(home, "data", "lgmon.db")
	s, err := store.Open(ctx, store.Options{Path: path, DisableMaintenance: true, Now: func() time.Time { return time.UnixMilli(at) }})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	mustExec := func(query string, args ...any) {
		t.Helper()
		if _, err := db.ExecContext(ctx, query, args...); err != nil {
			t.Fatal(err)
		}
	}
	mustExec("INSERT INTO collectors(id,hostname,first_seen,last_seen,version) VALUES('fake-a','fake A',?,?, 'test')", at-1000, at+30000)
	clockStore, err := store.Open(ctx, store.Options{Path: path, DisableMaintenance: true, Now: func() time.Time { return time.UnixMilli(at + 5000) }})
	if err != nil {
		t.Fatal(err)
	}
	for _, offset := range []int64{40000, 0} {
		if err := clockStore.SetClockOffset(ctx, "fake-a", offset); err != nil {
			_ = clockStore.Close()
			t.Fatal(err)
		}
	}
	if err := clockStore.Close(); err != nil {
		t.Fatal(err)
	}
	mustExec("INSERT INTO alerts(id,rule_id,collector_id,target_id,state,severity,started_at,summary) VALUES('fake-alert','fake-rule','fake-a','demo','firing','warning',?,'fake')", at)
	for _, event := range []struct {
		key, kind string
		ts        int64
	}{{"firing:old", "firing", at - 100}, {"firing:current", "firing", at + 1000}, {"renotify:current", "renotify", at + 2000}} {
		mustExec("INSERT INTO alert_events(alert_id,ts,kind,message,event_key) VALUES('fake-alert',?,?,'fake',?)", event.ts, event.kind, event.key)
	}
	for i, attempt := range []struct {
		key, notifier, state string
		ok, skipped          int
	}{
		{"firing:old", "fake-channel", "firing", 1, 0},
		{"firing:current", "other-channel", "firing", 1, 0},
		{"firing:current", "fake-channel", "resolved", 1, 0},
		{"renotify:current", "fake-channel", "firing", 1, 0},
		{"resume:fake-pause", "fake-channel", "firing", 1, 0},
		{"firing:current", "fake-channel", "firing", 0, 0},
		{"firing:current", "fake-channel", "firing", 1, 1},
	} {
		mustExec("INSERT INTO notifications(id,alert_id,notifier_id,template,ts,ok,state,event_key,skipped) VALUES(?,'fake-alert',?,'standard',?,?,?,?,?)",
			fmt.Sprintf("bad-%d", i), attempt.notifier, at+3000, attempt.ok, attempt.state, attempt.key, attempt.skipped)
	}
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	quote := func(s string) string { return "'" + strings.ReplaceAll(s, "'", "''") + "'" }
	wrapper := filepath.Join(dir, "cli.ps1")
	if err := os.WriteFile(wrapper, []byte("\ufeff& "+quote(exe)+" '-test.run=^TestM1AcceptanceCLIProcess$' '--' @args\n"), 0600); err != nil {
		t.Fatal(err)
	}
	scriptPath, err := filepath.Abs(filepath.Join("..", "..", "scripts", "m1-acceptance.ps1"))
	if err != nil {
		t.Fatal(err)
	}
	run := func(body string) map[string]any {
		t.Helper()
		script := filepath.Join(dir, "test.ps1")
		text := "\ufeff$ErrorActionPreference='Stop'\n[Console]::OutputEncoding=New-Object System.Text.UTF8Encoding\n. " + quote(scriptPath) + "\n"
		text += "$common=@{MonitorHome=" + quote(home) + ";LgmonPath=" + quote(wrapper) + ";CollectorID='fake-a';FromUtc='2033-05-18T03:33:20Z';TargetCount=2;ProbeCount=0;Now={ [DateTimeOffset]::FromUnixTimeMilliseconds(2000000060000) }}\n"
		text += body + " | ConvertTo-Json -Depth 24 -Compress\n"
		if err := os.WriteFile(script, []byte(text), 0600); err != nil {
			t.Fatal(err)
		}
		commandCtx, cancel := context.WithTimeout(ctx, 35*time.Second)
		defer cancel()
		cmd := exec.CommandContext(commandCtx, "powershell.exe", "-NoProfile", "-ExecutionPolicy", "Bypass", "-File", script)
		cmd.Env = append(os.Environ(), "LGMON_TEST_ACCEPTANCE_CLI=1")
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("acceptance failed: %v\n%s", err, out)
		}
		var result map[string]any
		if err := json.Unmarshal(out, &result); err != nil {
			t.Fatalf("invalid report: %s: %v", out, err)
		}
		return result
	}
	delivery := "Invoke-M1Acceptance @common -Mode Delivery -TargetID demo -RuleID fake-rule -NotifierID fake-channel"
	assertStatus := func(result map[string]any, want string) {
		t.Helper()
		if result["automaticStatus"] != want || result["manualAcceptance"] != "Pending" {
			t.Fatalf("false acceptance: %#v", result)
		}
	}
	assertStatus(run(delivery), "Failed")
	mustExec("INSERT INTO notifications(id,alert_id,notifier_id,template,ts,ok,state,event_key) VALUES('good','fake-alert','fake-channel','standard',?,1,'firing','firing:current')", at+15000)
	passed := run(delivery)
	assertStatus(passed, "Passed")
	if passed["evidence"].(map[string]any)["submissionDelayMS"] != float64(15000) {
		t.Fatalf("wrong clock or event: %#v", passed)
	}
	mustExec("UPDATE notifications SET ts=? WHERE id='good'", at+15001)
	assertStatus(run(delivery), "Failed")
	assertStatus(run("& { $early=$common.Clone(); $early.Now={ [DateTimeOffset]::FromUnixTimeMilliseconds(2000000010000) }; Invoke-M1Acceptance @early -Mode Delivery -TargetID demo -RuleID fake-rule -NotifierID fake-channel }"), "Pending")
	mustExec("INSERT INTO alerts(id,rule_id,collector_id,target_id,state,severity,started_at,summary) VALUES('offline','fake-offline','fake-a','@collector','firing','warning',?,'fake')", at)
	mustExec("INSERT INTO alert_events(alert_id,ts,kind,message,event_key) VALUES('offline',?,'firing','fake','firing:offline')", at+30000)
	mustExec("INSERT INTO notifications(id,alert_id,notifier_id,template,ts,ok,state,event_key) VALUES('offline-good','offline','fake-channel','standard',?,1,'firing','firing:offline')", at+35000)
	offline := "Invoke-M1Acceptance @common -Mode Delivery -Scenario Offline -RuleID fake-offline -NotifierID fake-channel"
	assertStatus(run(offline), "Passed")
	mustExec("UPDATE notifications SET ts=? WHERE id='offline-good'", at+35001)
	assertStatus(run(offline), "Failed")
	for i, state := range []string{"up", "down", "up"} {
		mustExec("INSERT INTO target_status(collector_id,target_id,ts,state,detail) VALUES('fake-a','demo',?,?,'{}')", at+int64(i)*10000, state)
	}
	history := "Invoke-M1Acceptance @common -Mode History -TargetID demo -ToUtc '2033-05-18T03:34:00Z'"
	passed = run(history)
	assertStatus(passed, "Passed")
	transitions := passed["evidence"].(map[string]any)["transitions"].([]any)
	if len(transitions) != 3 || transitions[1].(map[string]any)["ts"] != float64(at+10000) {
		t.Fatalf("source history changed: %#v", passed)
	}
	historyEvidence := passed["evidence"].(map[string]any)
	if len(historyEvidence["gaps"].([]any)) != 0 || len(historyEvidence["clockEvents"].([]any)) != 2 {
		t.Fatalf("clock events misclassified or lost: %#v", historyEvidence)
	}
	mustExec("INSERT INTO collector_events(collector_id,seq,ts,kind,message,gap_from,gap_to,dropped) VALUES('fake-a',1,?,'data_gap','fake',1,2,2)", at+30000)
	gapReport := run(history)
	assertStatus(gapReport, "Failed")
	gapRows := gapReport["evidence"].(map[string]any)["gaps"].([]any)
	if len(gapRows) != 1 || gapRows[0].(map[string]any)["kind"] != "data_gap" {
		t.Fatal("actual data_gap not retained")
	}
	mustExec("DELETE FROM collector_events WHERE collector_id='fake-a' AND seq=1")
	mustExec("INSERT INTO collector_events(collector_id,seq,ts,kind,message,gap_from,gap_to,dropped) VALUES('fake-a',-3,?,'late_dropped','fake',0,0,1)", at+30000)
	gapReport = run(history)
	assertStatus(gapReport, "Failed")
	gapRows = gapReport["evidence"].(map[string]any)["gaps"].([]any)
	if len(gapRows) != 1 || gapRows[0].(map[string]any)["kind"] != "late_dropped" {
		t.Fatal("late_dropped not retained")
	}
	for i := 0; i < 2; i++ {
		mustExec("INSERT INTO process_samples(collector_id,target_id,ts,pid,cpu,rss,created_at) VALUES('fake-a','self',?,42,?,?,1)", at+int64(i)*2000, float64(i+1)*0.2, (i+2)*1024*1024)
	}
	resources := run("Invoke-M1Acceptance @common -Mode Resources -TargetID self -ToUtc '2033-05-18T03:34:00Z'")
	assertStatus(resources, "Pending")
	stats := resources["evidence"].(map[string]any)
	if stats["samples"] != float64(2) || stats["rssMaxBytes"] != float64(3*1024*1024) || stats["maxGapMs"] != float64(2000) || stats["cpuAveragePercent"] != 0.4 {
		t.Fatalf("invalid resource statistics: %#v", stats)
	}
	mustExec("INSERT INTO process_samples(collector_id,target_id,ts,pid,cpu,rss,created_at) VALUES('fake-a','self',?,43,0.2,1048576,2)", at)
	resources = run("Invoke-M1Acceptance @common -Mode Resources -TargetID self -ToUtc '2033-05-18T03:34:00Z'")
	stats = resources["evidence"].(map[string]any)
	if stats["ambiguousSamples"] != float64(1) {
		t.Fatal("self-monitor matched multiple processes without warning")
	}
	mustExec("INSERT INTO process_samples(collector_id,target_id,ts,pid,cpu,rss,created_at) VALUES('fake-a','high-rss',?,42,0.2,41943040,1)", at)
	assertStatus(run("Invoke-M1Acceptance @common -Mode Resources -TargetID high-rss -ToUtc '2033-05-18T03:34:00Z'"), "Failed")
	assertStatus(run(`& {
 $baseline=[PSCustomObject]@{
  samples=43200;first_ts=2000000000000;last_ts=2000086398000;max_gap_ms=2000;ambiguous_samples=0
  identities_json='[{"pid":42,"createdAt":1}]';nonpositive_json='[]';cpu_average_percent=0.5
  rss_max_bytes=10485760;rss_max_at=2000000000000;rss_average_bytes=10485760
  rss_first_hour_average_bytes=10485760;rss_last_hour_average_bytes=10485760
 }
 $cases=@(
  @{name='complete';want='Passed';change={param($s)}},
  @{name='cpu-limit';want='Failed';change={param($s) $s.cpu_average_percent=1.0}},
  @{name='cpu-below';want='Passed';change={param($s) $s.cpu_average_percent=0.999999}},
  @{name='rss-limit';want='Failed';change={param($s) $s.rss_max_bytes=41943040}},
  @{name='rss-below';want='Passed';change={param($s) $s.rss_max_bytes=41943039}},
  @{name='short-window';want='Pending';to=2000086399999;change={param($s)}},
  @{name='short-high-rss';want='Failed';to=2000086399999;change={param($s) $s.rss_max_bytes=41943040}},
  @{name='restart';want='Pending';change={param($s) $s.identities_json='[{"pid":42,"createdAt":1},{"pid":43,"createdAt":2}]'}},
  @{name='started-at-window';want='Pending';change={param($s) $s.identities_json='[{"pid":42,"createdAt":2000000000000}]'}},
  @{name='started-before-window';want='Passed';change={param($s) $s.identities_json='[{"pid":42,"createdAt":1999999999999}]'}},
  @{name='unknown-start';want='Pending';change={param($s) $s.identities_json='[{"pid":42,"createdAt":0}]'}},
  @{name='ambiguous';want='Pending';change={param($s) $s.ambiguous_samples=1}},
  @{name='first-outside';want='Pending';change={param($s) $s.first_ts=2000000010001}},
  @{name='first-boundary';want='Passed';change={param($s) $s.first_ts=2000000010000}},
  @{name='last-outside';want='Pending';change={param($s) $s.last_ts=2000086389999}},
  @{name='last-boundary';want='Passed';change={param($s) $s.last_ts=2000086390000}},
  @{name='long-gap';want='Pending';change={param($s) $s.max_gap_ms=30001}},
  @{name='gap-boundary';want='Passed';change={param($s) $s.max_gap_ms=30000}},
  @{name='count-below';want='Pending';change={param($s) $s.samples=41039}},
  @{name='count-boundary';want='Passed';change={param($s) $s.samples=41040}},
  @{name='configured-four-seconds';want='Passed';change={param($s) $s|Add-Member interval_count 1;$s|Add-Member interval_min_ms 4000;$s|Add-Member interval_max_ms 4000;$s.samples=21600}},
  @{name='configured-four-seconds-insufficient';want='Pending';change={param($s) $s|Add-Member interval_count 1;$s|Add-Member interval_min_ms 4000;$s|Add-Member interval_max_ms 4000;$s.samples=20519}},
  @{name='mixed-configurations';want='Pending';change={param($s) $s|Add-Member interval_count 2;$s|Add-Member interval_min_ms 2000;$s|Add-Member interval_max_ms 4000}},
  @{name='unknown-configured-period';want='Pending';change={param($s) $s|Add-Member interval_count 0;$s|Add-Member interval_min_ms $null;$s|Add-Member interval_max_ms $null}},
  @{name='data-gap';want='Pending';events=@([PSCustomObject]@{kind='data_gap'});change={param($s)}},
  @{name='late-dropped';want='Pending';events=@([PSCustomObject]@{kind='late_dropped'});change={param($s)}},
  @{name='clock-events';want='Passed';events=@([PSCustomObject]@{kind='clock_skew'},[PSCustomObject]@{kind='clock_synced'});change={param($s)}},
  @{name='incomplete-high-cpu';want='Pending';change={param($s) $s.cpu_average_percent=1.5;$s.samples=100}},
  @{name='no-cpu-interval';want='Pending';change={param($s) $s.cpu_average_percent=$null}},
  @{name='no-samples';want='Pending';change={param($s) $s.samples=0;$s.identities_json='[]';$s.first_ts=$null;$s.last_ts=$null;$s.cpu_average_percent=$null;$s.rss_max_bytes=$null}}
 )
 foreach ($case in $cases) {
  $statistics=$baseline.PSObject.Copy()
  & $case.change $statistics
  $events=@();if ($case.ContainsKey('events')) { $events=$case.events }
  $end=2000086400000;if ($case.ContainsKey('to')) { $end=$case.to }
  $result=New-M1ResourceResult -Statistics $statistics -Events $events -From 2000000000000 -To $end -TargetCount 2 -ProbeCount 0
  if ($result.automaticStatus -ne $case.want) { throw ($case.name + ': ' + ($result | ConvertTo-Json -Depth 12 -Compress)) }
  if (($case.want -eq 'Passed') -ne ($result.evidence.reasons.Count -eq 0)) { throw ('Missing reasons: ' + $case.name) }
  $period=2000;if($case.name -like 'configured-four-seconds*'){$period=4000};if($case.name -in @('mixed-configurations','unknown-configured-period')){$period=$null}
  if ($result.evidence.thresholds.rssBytes -ne 41943040 -or $result.evidence.targetIntervalMs -ne $period) { throw 'Wrong resource thresholds.' }
 }
 [PSCustomObject]@{automaticStatus='Passed';manualAcceptance='Pending'}
}`), "Passed")
	mustExec("INSERT INTO collectors(id,hostname,first_seen,last_seen,version) VALUES('fake-resource','fake resource',?,?, 'test')", at-1000, at+86400000)
	mustExec(`WITH RECURSIVE ticks(i) AS (VALUES(0) UNION ALL SELECT i+1 FROM ticks WHERE i<43199)
INSERT INTO process_samples(collector_id,target_id,ts,pid,cpu,rss,created_at)
SELECT 'fake-resource','self-complete',?+i*2000,42,CASE WHEN i=0 THEN 100.0 ELSE 0.5 END,
CASE WHEN i<1800 THEN 10485760 WHEN i>=41400 THEN 31457280 ELSE 20971520 END,?-10000 FROM ticks`, at, at)
	whole := `& { $whole=$common.Clone();$whole.CollectorID='fake-resource';$whole.Now={ [DateTimeOffset]::FromUnixTimeMilliseconds(2000086401000) };Invoke-M1Acceptance @whole -Mode Resources -TargetID self-complete -ToUtc '2033-05-19T03:33:20Z' }`
	complete := run(whole)
	assertStatus(complete, "Passed")
	stats = complete["evidence"].(map[string]any)
	for key, want := range map[string]float64{
		"samples": 43200, "expectedSamples": 43200, "windowMs": 86400000, "cpuAveragePercent": 0.5,
		"rssMaxBytes": 31457280, "rssMaxAt": float64(at + 82800000), "rssAverageBytes": 20971520,
		"rssFirstHourAverageBytes": 10485760, "rssLastHourAverageBytes": 31457280, "targetCount": 2, "probeCount": 0,
	} {
		if stats[key] != want {
			t.Fatalf("incorrect complete resource report %s: got %v, want %v", key, stats[key], want)
		}
	}
	for _, value := range []struct {
		ts  int64
		cpu float64
	}{{0, 100}, {1000, 0.1}, {4000, 0.9}, {3000, 100}, {6000, 0.2}, {6000, 100}} {
		mustExec("INSERT INTO process_samples(collector_id,target_id,ts,pid,cpu,rss,created_at) VALUES('fake-a','weighted',?,42,?,10485760,1)", at+value.ts, value.cpu)
	}
	weighted := run("Invoke-M1Acceptance @common -Mode Resources -TargetID weighted -ToUtc '2033-05-18T03:34:00Z'")
	assertStatus(weighted, "Pending")
	stats = weighted["evidence"].(map[string]any)
	if math.Abs(stats["cpuAveragePercent"].(float64)-3400.0/7000) > 1e-12 || len(stats["nonPositiveIntervals"].([]any)) != 2 || stats["ambiguousSamples"] != float64(0) {
		t.Fatalf("rollback or time weighting is wrong: %#v", stats)
	}
	assertStatus(run(`& {
 $cases=@(
  { ConvertTo-M1Time '2033-05-18T03:33:20' 'FromUtc' },
  { Invoke-M1Acceptance @common -Mode History -TargetID demo -ToUtc '2033-05-18T03:35:00Z' },
  { Invoke-M1Acceptance @common -Mode Delivery -RuleID fake-rule -NotifierID fake-channel },
  { Invoke-M1Acceptance @common -Mode Delivery -Scenario Offline -State resolved -RuleID fake-offline -NotifierID fake-channel },
  { Invoke-M1Acceptance @common -Mode Snapshot -CheckOnly }
 )
 foreach ($case in $cases) {
  $rejected=$false
  try { & $case | Out-Null } catch { $rejected=$true }
  if (-not $rejected) { throw 'Invalid acceptance input was accepted.' }
 }
 function Invoke-M1CLI { [PSCustomObject]@{truncated=$true;columns=@('ts');rows=@()} }
 $rejected=$false
 try { Invoke-M1Query -Executable 'unused' -MonitorHome 'unused' -SQL 'SELECT 1' -From 1 -To 2 | Out-Null } catch { $rejected=$true }
 if (-not $rejected) { throw 'Truncated history was accepted.' }
 [PSCustomObject]@{automaticStatus='Passed';manualAcceptance='Pending'}
}`), "Passed")
	// SQL literals must remain a single value even when the input is not a configured target ID.
	injected := run("Invoke-M1Acceptance @common -Mode History -TargetID \"demo' OR 1=1 --\" -ToUtc '2033-05-18T03:34:00Z'")
	if len(injected["evidence"].(map[string]any)["transitions"].([]any)) != 0 {
		t.Fatal("target text was interpreted as SQL")
	}
	emptyHome := filepath.Join(dir, "empty")
	if err := os.MkdirAll(filepath.Join(emptyHome, "config"), 0700); err != nil {
		t.Fatal(err)
	}
	result := run("Invoke-M1Acceptance -MonitorHome " + quote(emptyHome) + " -LgmonPath " + quote(wrapper) + " -CheckOnly")
	assertStatus(result, "Passed")
	if result["databaseAvailable"] != false {
		t.Fatal("no-data preflight reported a database")
	}
	if _, err := os.Stat(filepath.Join(emptyHome, "data")); !os.IsNotExist(err) {
		t.Fatal("read-only preflight created a database directory")
	}
	var notifications, events int
	if err := db.QueryRow("SELECT count(*) FROM notifications").Scan(&notifications); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow("SELECT count(*) FROM alert_events").Scan(&events); err != nil {
		t.Fatal(err)
	}
	if notifications != 9 || events != 4 {
		t.Fatal("acceptance created notification or alert events")
	}
}
