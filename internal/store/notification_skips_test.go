package store

import (
	"context"
	"database/sql"
	"io/fs"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/LFenX/lg-monitor/internal/notify"
)

func TestSkippedNotificationsAreSeparateFromDeliveryAndNotReplayed(t *testing.T) {
	ctx := context.Background()
	s := openStore(t, Options{})
	alert := testAlert()
	event := testEvent(alert)
	if err := s.SaveTransition(ctx, alert, event); err != nil {
		t.Fatal(err)
	}
	skip := notify.Attempt{ID: "fake-skip", AlertID: alert.ID, State: alert.State, NotifierID: "desktop", TS: max(event.TS, alert.LastNotifiedAt) + 1, Skipped: true, Error: "没有登录用户"}
	if err := s.RecordNotification(ctx, skip); err != nil {
		t.Fatal(err)
	}
	var last int64
	if err := s.db.QueryRow(`SELECT coalesce(last_notified_at,0) FROM alerts WHERE id=?`, alert.ID).Scan(&last); err != nil || last != alert.LastNotifiedAt {
		t.Fatalf("skip pretended to deliver: %d, %v", last, err)
	}
	items, err := s.PendingNotifications(ctx, 0, skip.TS, 0, 10)
	if err != nil || len(items) != 1 || len(items[0].Successful) != 0 || !reflect.DeepEqual(items[0].Skipped, []string{"desktop"}) {
		t.Fatalf("skipped destinations=%+v,%v", items, err)
	}
	accepted := notify.Attempt{ID: "fake-webhook-accepted", AlertID: alert.ID, State: alert.State, NotifierID: "hook", TS: skip.TS + 1, OK: true, StatusCode: 202}
	if err := s.RecordNotification(ctx, accepted); err != nil {
		t.Fatalf("channel without provider message ID cannot be recorded: %v", err)
	}
	var status int
	if err := s.db.QueryRow(`SELECT status_code FROM notifications WHERE id=?`, accepted.ID).Scan(&status); err != nil || status != 202 {
		t.Fatalf("HTTP status not persisted: %d %v", status, err)
	}
	if err := s.db.QueryRow(`SELECT last_notified_at FROM alerts WHERE id=?`, alert.ID).Scan(&last); err != nil || last != accepted.TS {
		t.Fatalf("accepted channel not recorded: %d, %v", last, err)
	}
	invalid := skip
	invalid.ID, invalid.OK = "fake-conflict", true
	if err := s.RecordNotification(ctx, invalid); err == nil {
		t.Fatal("success and skip accepted together")
	}
	invalid.OK, invalid.Error = false, ""
	if err := s.RecordNotification(ctx, invalid); err == nil {
		t.Fatal("skip without explanation accepted")
	}
}

func TestUpgradePreservesExistingNotificationReceipts(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "upgrade-notifications.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(`CREATE TABLE schema_migrations(version INTEGER PRIMARY KEY,applied_at INTEGER NOT NULL)`); err != nil {
		t.Fatal(err)
	}
	files, err := fs.Glob(migrations, "migrations/*.sql")
	if err != nil {
		t.Fatal(err)
	}
	now := func() time.Time { return time.UnixMilli(2000) }
	for _, file := range files {
		version, err := strconv.Atoi(strings.SplitN(filepath.Base(file), "_", 2)[0])
		if err != nil {
			t.Fatal(err)
		}
		if version <= 8 {
			if err := applyMigration(ctx, db, file, version, now); err != nil {
				t.Fatal(err)
			}
		}
	}
	if _, err := db.Exec(`INSERT INTO notifications(id,alert_id,notifier_id,ts,ok,error,message_id,state,event_key) VALUES('fake-old','fake-alert','fake-channel',1500,1,'','om_fake_old','firing','fake-event')`); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	s := openStore(t, Options{Path: path, Now: now, DisableMaintenance: true})
	var ok, skipped bool
	var messageID string
	if err := s.db.QueryRow(`SELECT ok,skipped,message_id FROM notifications WHERE id='fake-old'`).Scan(&ok, &skipped, &messageID); err != nil || !ok || skipped || messageID != "om_fake_old" {
		t.Fatalf("upgrade changed old receipt: ok=%v skipped=%v id=%q err=%v", ok, skipped, messageID, err)
	}
}
