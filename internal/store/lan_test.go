package store

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"testing"
	"time"
)

func TestLANRequestsCapExpireAndDecideOnce(t *testing.T) {
	ctx := context.Background()
	now := time.UnixMilli(2_000_000_000_000)
	s, err := OpenAuthDB(ctx, filepath.Join(t.TempDir(), "auth.db"), func() time.Time { return now })
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	add := func(id string) error {
		_, err := s.AddLANRequest(ctx, LANRequest{ID: id, BoardID: "main", DeviceName: "虚构设备", Address: "192.0.2.1", Code: "123 456", SecretHash: []byte("fake-hash")}, 2, now.Add(-24*time.Hour).UnixMilli())
		return err
	}
	for index := range 2 {
		if err := add(fmt.Sprint("lanreq-", index)); err != nil {
			t.Fatal(err)
		}
	}
	if err := add("lanreq-2"); !errors.Is(err, ErrTeamInput) {
		t.Fatalf("a third pending request must be refused, got %v", err)
	}
	if err := s.DecideLANRequest(ctx, "lanreq-0", LANDenied, "", nil); err != nil {
		t.Fatal(err)
	}
	if err := s.DecideLANRequest(ctx, "lanreq-0", LANApproved, "member-fake", nil); !errors.Is(err, ErrLANNotFound) {
		t.Fatalf("a decided request must not be decided again, got %v", err)
	}
	// A day later the remaining pending request expires and frees its slot.
	now = now.Add(25 * time.Hour)
	list, err := s.LANRequests(ctx, now.Add(-24*time.Hour).UnixMilli(), 0)
	if err != nil || len(list) != 2 {
		t.Fatalf("list=%+v %v", list, err)
	}
	for _, item := range list {
		if item.ID == "lanreq-1" && item.Status != LANExpired {
			t.Fatalf("old pending request did not expire: %+v", item)
		}
	}
	first, err := s.AddLANGrant(ctx, LANGrant{ID: "grant-a", MemberID: "member-fake", BoardID: "main", DeviceName: "虚构设备"})
	if err != nil {
		t.Fatal(err)
	}
	again, err := s.AddLANGrant(ctx, LANGrant{ID: "grant-b", MemberID: "member-fake", BoardID: "main", DeviceName: "虚构设备"})
	if err != nil || again.ID != first.ID {
		t.Fatalf("the same device and board must keep one grant: %+v %v", again, err)
	}
	if granted, _ := s.LANGranted(ctx, "member-fake", "main"); !granted {
		t.Fatal("grant not visible")
	}
	if _, err := s.RevokeLANGrant(ctx, first.ID); err != nil {
		t.Fatal(err)
	}
	if granted, _ := s.LANGranted(ctx, "member-fake", "main"); granted {
		t.Fatal("revoked grant still allows reading")
	}
	if _, err := s.RevokeLANGrant(ctx, first.ID); !errors.Is(err, ErrLANNotFound) {
		t.Fatalf("revoking twice must report not found, got %v", err)
	}
}
