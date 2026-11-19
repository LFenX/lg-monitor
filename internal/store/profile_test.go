package store

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"
)

func TestProfileIsCreatedOnceAndSaved(t *testing.T) {
	ctx := context.Background()
	clock := time.UnixMilli(1_000)
	db, err := OpenAuthDB(ctx, filepath.Join(t.TempDir(), "auth.db"), func() time.Time { return clock })
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Profile(ctx); !errors.Is(err, ErrNoProfile) {
		t.Fatalf("Profile before creation = %v", err)
	}
	if _, err := db.SaveProfile(ctx, "小李", "fiora-1"); !errors.Is(err, ErrNoProfile) {
		t.Fatalf("SaveProfile before creation = %v", err)
	}
	first, err := db.CreateProfile(ctx, Profile{Nickname: "安静的水獭", Avatar: "fiora-3"})
	if err != nil || first.Nickname != "安静的水獭" || first.Avatar != "fiora-3" || first.CreatedAt != 1_000 {
		t.Fatalf("CreateProfile = %+v, %v", first, err)
	}
	second, err := db.CreateProfile(ctx, Profile{Nickname: "别的名字", Avatar: "huaji-1"})
	if err != nil || second != first {
		t.Fatalf("a second CreateProfile replaced the account: %+v, %v", second, err)
	}
	clock = time.UnixMilli(2_000)
	saved, err := db.SaveProfile(ctx, "小李", "huaji-18")
	if err != nil || saved.Nickname != "小李" || saved.Avatar != "huaji-18" || saved.CreatedAt != 1_000 || saved.UpdatedAt != 2_000 {
		t.Fatalf("SaveProfile = %+v, %v", saved, err)
	}
}

func TestOldLANTablesGainAvatarColumns(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "auth.db")
	db, err := OpenAuthDB(ctx, path, func() time.Time { return time.UnixMilli(1) })
	if err != nil {
		t.Fatal(err)
	}
	// Make the tables look like 0.13 wrote them, then open again.
	for _, statement := range []string{`ALTER TABLE lan_requests DROP COLUMN avatar`, `ALTER TABLE lan_grants DROP COLUMN avatar`, `ALTER TABLE lan_outgoing DROP COLUMN owner_avatar`, `INSERT INTO lan_grants(id,member_id,board_id,device_name,hostname,created_at) VALUES('g1','m1','b1','旧设备','HOST',1)`} {
		if _, err := db.db.ExecContext(ctx, statement); err != nil {
			t.Fatalf("%s: %v", statement, err)
		}
	}
	db.Close()
	db, err = OpenAuthDB(ctx, path, func() time.Time { return time.UnixMilli(2) })
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	grants, err := db.LANGrants(ctx)
	if err != nil || len(grants) != 1 || grants[0].Avatar != "" || grants[0].DeviceName != "旧设备" {
		t.Fatalf("LANGrants after upgrade = %+v, %v", grants, err)
	}
	if err := db.UpdateLANOutgoingOwner(ctx, "missing", "a", "fiora-1"); err != nil {
		t.Fatal(err)
	}
	var count int
	if err := db.db.QueryRowContext(ctx, `SELECT count(*) FROM pragma_table_info('lan_outgoing') WHERE name='owner_avatar'`).Scan(&count); err != nil || count != 1 {
		t.Fatalf("owner_avatar column: %d, %v", count, err)
	}
}
