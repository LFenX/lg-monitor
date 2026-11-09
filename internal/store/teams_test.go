package store

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestTeamAuthenticationInvitesScopesRevocationAndLocalMetadata(t *testing.T) {
	ctx := context.Background()
	now := time.UnixMilli(2000000000000)
	s, err := OpenAuthDB(ctx, filepath.Join(t.TempDir(), "auth.db"), func() time.Time { return now })
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	for _, team := range []string{"team-one", "team-other"} {
		if _, err := s.CreateTeam(ctx, Team{ID: team, Name: "虚构团队", OwnerName: "虚构负责人"}); err != nil {
			t.Fatal(err)
		}
	}
	hash := strings.Repeat("a", 64)
	if err := s.AddTeamInvite(ctx, hash, "team-one", "虚构成员", now.Add(time.Hour).UnixMilli()); err != nil {
		t.Fatal(err)
	}
	member, err := s.ConsumeTeamInvite(ctx, hash, TeamMember{ID: "member-one", Serial: "1"})
	if err != nil || member.TeamID != "team-one" {
		t.Fatal(member, err)
	}
	if _, err := s.ConsumeTeamInvite(ctx, hash, TeamMember{ID: "member-other", Serial: "2"}); !errors.Is(err, ErrTeamInvite) {
		t.Fatal("invite replay", err)
	}
	if _, err := s.TeamMember(ctx, member.ID, "different"); !errors.Is(err, ErrTeamIdentity) {
		t.Fatal("serial mismatch", err)
	}
	share, err := s.SaveTeamShare(ctx, TeamShare{ID: "share-one", TeamID: "team-one", DashboardID: "dashboard", Panels: []string{"first"}, ShareScope: ShareScope{Collectors: []string{"fake-host"}, Targets: []string{"web"}}, Members: []string{member.ID}})
	if err != nil || share.Revision != 1 {
		t.Fatal(share, err)
	}
	if _, err := s.MemberShare(ctx, share.ID, TeamMember{ID: "member-not-in-share", TeamID: "team-one"}); !errors.Is(err, ErrTeamNotFound) {
		t.Fatal("nonselected member read share", err)
	}
	if _, err := s.MemberShare(ctx, share.ID, TeamMember{ID: member.ID, TeamID: "team-other"}); !errors.Is(err, ErrTeamNotFound) {
		t.Fatal("cross-team read", err)
	}
	if _, err := s.MemberShare(ctx, share.ID, member); err != nil {
		t.Fatal(err)
	}
	if err := s.VisitTeam(ctx, member.ID, member.Serial); err != nil {
		t.Fatal(err)
	}
	members, err := s.TeamMembers(ctx, "team-one")
	if err != nil || len(members) != 1 || members[0].AccessCount != 1 || members[0].LastAccess != now.UnixMilli() {
		t.Fatal(members, err)
	}
	updated, err := s.SaveTeamShare(ctx, share)
	if err != nil || updated.Revision != 2 {
		t.Fatal("share update", updated, err)
	}
	if _, err := s.SaveTeamShare(ctx, share); !errors.Is(err, ErrTeamConflict) {
		t.Fatal("stale scope overwrote latest", err)
	}
	if err := s.RevokeTeamShare(ctx, share.ID); err != nil {
		t.Fatal(err)
	}
	if err := s.RevokeTeamShare(ctx, share.ID); err != nil {
		t.Fatal("idempotent revoke", err)
	}
	if _, err := s.MemberShare(ctx, share.ID, member); !errors.Is(err, ErrTeamNotFound) {
		t.Fatal("revoked share visible", err)
	}
	if err := s.RevokeTeamMember(ctx, "team-one", member.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.TeamMember(ctx, member.ID, member.Serial); !errors.Is(err, ErrTeamIdentity) {
		t.Fatal("revoked member active", err)
	}
	if err := s.VisitTeam(ctx, member.ID, member.Serial); !errors.Is(err, ErrTeamIdentity) {
		t.Fatal("revoked visit recorded", err)
	}
	joined := JoinedTeam{ID: "joined-one", TeamID: "team-one", TeamName: "虚构远端", OwnerName: "虚构负责人", MemberID: member.ID, Address: "https://127.0.0.1:7445", Fingerprint: strings.Repeat("b", 64), CA: []byte("fake-ca"), Certificate: []byte("fake-public-cert"), KeyRef: "fake-protected-key"}
	joined, err = s.AddJoinedTeam(ctx, joined)
	if err != nil {
		t.Fatal(err)
	}
	changed, err := s.ChangeJoinedAddress(ctx, joined.ID, "https://localhost:8445")
	if err != nil || changed.Fingerprint != joined.Fingerprint || changed.MemberID != joined.MemberID || string(changed.Certificate) != string(joined.Certificate) || changed.KeyRef != joined.KeyRef {
		t.Fatal("address changed identity", changed, err)
	}
	if _, err := s.AddJoinedTeam(ctx, JoinedTeam{ID: "joined-duplicate", TeamID: joined.TeamID, MemberID: joined.MemberID, Address: joined.Address, Fingerprint: joined.Fingerprint, CA: joined.CA, Certificate: joined.Certificate, KeyRef: joined.KeyRef}); err == nil {
		t.Fatal("same device joined same CA/team twice")
	}
	if err := s.LeaveJoinedTeam(ctx, joined.ID); err != nil {
		t.Fatal(err)
	}
	values, err := s.JoinedTeams(ctx)
	if err != nil || len(values) != 0 {
		t.Fatal(values, err)
	}
}

func TestTeamInviteExpiryAtomicRollbackAndConcurrentConsumption(t *testing.T) {
	ctx := context.Background()
	now := time.UnixMilli(2000000000000)
	s, err := OpenAuthDB(ctx, filepath.Join(t.TempDir(), "auth.db"), func() time.Time { return now })
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if _, err := s.CreateTeam(ctx, Team{ID: "team-fixture", Name: "测试团队", OwnerName: "负责人"}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ConsumeTeamInvite(ctx, strings.Repeat("0", 64), TeamMember{ID: "member-missing", Serial: "1"}); !errors.Is(err, ErrTeamInvite) {
		t.Fatal("unknown invitation accepted", err)
	}
	expired := strings.Repeat("1", 64)
	if err := s.AddTeamInvite(ctx, expired, "team-fixture", "过期成员", now.Add(time.Second).UnixMilli()); err != nil {
		t.Fatal(err)
	}
	now = now.Add(time.Second)
	if _, err := s.ConsumeTeamInvite(ctx, expired, TeamMember{ID: "member-expired", Serial: "2"}); !errors.Is(err, ErrTeamInvite) {
		t.Fatal("expired invitation accepted", err)
	}
	first, second := strings.Repeat("2", 64), strings.Repeat("3", 64)
	for _, hash := range []string{first, second} {
		if err := s.AddTeamInvite(ctx, hash, "team-fixture", "并发成员", now.Add(time.Hour).UnixMilli()); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.ConsumeTeamInvite(ctx, first, TeamMember{ID: "member-existing", Serial: "3"}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ConsumeTeamInvite(ctx, second, TeamMember{ID: "member-existing", Serial: "4"}); err == nil {
		t.Fatal("duplicate identity accepted")
	}
	if _, err := s.ConsumeTeamInvite(ctx, second, TeamMember{ID: "member-after-rollback", Serial: "4"}); err != nil {
		t.Fatal("failed member insert consumed invitation", err)
	}
	concurrent := strings.Repeat("4", 64)
	if err := s.AddTeamInvite(ctx, concurrent, "team-fixture", "并发唯一", now.Add(time.Hour).UnixMilli()); err != nil {
		t.Fatal(err)
	}
	start := make(chan struct{})
	results := make(chan error, 2)
	var workers sync.WaitGroup
	for _, id := range []string{"member-race-one", "member-race-two"} {
		workers.Add(1)
		go func() {
			defer workers.Done()
			<-start
			_, err := s.ConsumeTeamInvite(ctx, concurrent, TeamMember{ID: id, Serial: id})
			results <- err
		}()
	}
	close(start)
	workers.Wait()
	close(results)
	success := 0
	for err := range results {
		if err == nil {
			success++
		} else if !errors.Is(err, ErrTeamInvite) {
			t.Fatal("unexpected concurrent failure", err)
		}
	}
	if success != 1 {
		t.Fatal("one invitation registered multiple devices", success)
	}
	members, err := s.TeamMembers(ctx, "team-fixture")
	if err != nil || len(members) != 3 {
		t.Fatal("invalid invitation changed roster", len(members), err)
	}
}
