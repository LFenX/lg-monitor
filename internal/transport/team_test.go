package transport

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"testing"
	"time"

	lgmonv1 "github.com/LFenX/lg-monitor/proto/lgmon/v1"
)

type teamHandshakeFixture struct {
	authority  *Authority
	requests   []string
	unsafeCode bool
	team       string
}

func (f *teamHandshakeFixture) Exchange(_ context.Context, _ string, path string, configuration *tls.Config, input, output any) error {
	f.requests = append(f.requests, path)
	if configuration.InsecureSkipVerify && input != nil {
		f.unsafeCode = true
		return fmt.Errorf("invitation sent without CA pin")
	}
	switch path {
	case "/team/v1/authority":
		if input != nil {
			return fmt.Errorf("bootstrap body contains credentials")
		}
		*output.(*TeamAuthorityResponse) = TeamAuthorityResponse{CAPEM: f.authority.CAPEM()}
	case "/team/v1/join":
		if configuration.InsecureSkipVerify || configuration.RootCAs == nil {
			return fmt.Errorf("join is not pinned")
		}
		request := input.(TeamJoinRequest)
		id, certificate, err := f.authority.SignTeamCSR(request.CSRDER)
		if err != nil {
			return err
		}
		*output.(*TeamJoinResponse) = TeamJoinResponse{TeamID: f.team, TeamName: "测试团队", OwnerName: "测试负责人", MemberID: id, CAPEM: f.authority.CAPEM(), CertificatePEM: certificate}
	default:
		return fmt.Errorf("unexpected path")
	}
	return nil
}

func TestTeamJoinPinsBeforeSendingInvitationAndSeparatesMemberIdentity(t *testing.T) {
	f := newPairingFixture(t)
	team := "team-" + strings.Repeat("a", 32)
	text, hash, err := NewTeamInvitation("https://monitor.test:7445", f.authority.Fingerprint(), team, nil)
	if err != nil {
		t.Fatal(err)
	}
	invitation, err := ParseTeamInvitation(text)
	if err != nil || hash != TeamInviteHash(invitation.Code) {
		t.Fatal("invitation round trip", err)
	}
	connector := &teamHandshakeFixture{authority: f.authority, team: team}
	keys := newMemoryKeys()
	if _, _, _, err := JoinTeam(f.ctx, invitation, "", connector, keys, nil, f.now); err == nil {
		t.Fatal("missing human pin accepted")
	}
	if _, _, _, err := JoinTeam(f.ctx, invitation, strings.Repeat("0", 16), connector, keys, nil, f.now); !errors.Is(err, ErrFingerprint) {
		t.Fatal("wrong pin accepted", err)
	}
	if len(keys.data) != 0 || len(connector.requests) != 2 || connector.unsafeCode {
		t.Fatal("failed confirmation sent code or saved key", connector.requests)
	}
	response, ref, fingerprint, err := JoinTeam(f.ctx, invitation, invitation.Fingerprint, connector, keys, nil, f.now)
	if err != nil || response.TeamID != team || len(keys.data) != 1 || connector.unsafeCode {
		t.Fatal("pinned join", response, err)
	}
	clientTLS, err := LoadTeamTLS(f.ctx, keys, response.CAPEM, response.CertificatePEM, ref, fingerprint, f.now)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := LoadTeamTLS(f.ctx, keys, response.CAPEM, response.CertificatePEM, ref, strings.Repeat("0", 16), f.now); !errors.Is(err, ErrFingerprint) {
		t.Fatal("changed CA pin accepted", err)
	}
	if _, csr, err := NewClientRequest("collector-fixture", nil); err != nil {
		t.Fatal(err)
	} else if _, _, err := f.authority.SignTeamCSR(csr); err == nil {
		t.Fatal("collector CSR became member")
	}
	ctx, cancel := context.WithTimeout(f.ctx, 2*time.Second)
	defer cancel()
	connection, err := f.connector.Connect(ctx, "monitor.test:7443", clientTLS)
	if err != nil {
		t.Fatal(err)
	}
	defer connection.Close()
	stream, err := lgmonv1.NewPairingClient(connection).TeamAdmin(ctx, &lgmonv1.TeamAdminRequest{Operation: "list_teams"})
	if err == nil {
		_, err = stream.Recv()
	}
	if err == nil {
		t.Fatal("member certificate managed collector API")
	}
	if _, err := NewWriter(ctx, connection, Hello{CollectorID: response.MemberID}); err == nil {
		t.Fatal("member certificate ingested collector samples")
	}
}

func TestTeamAdminStreamsLargeResultsWithoutIncreasingFrameBudget(t *testing.T) {
	f := newPairingFixture(t)
	data, _ := json.Marshal(map[string]string{"value": strings.Repeat("x", 5*1024*1024)})
	panelID := strings.Repeat("card-", 850)
	f.receiver.SetTeamAdmin(func(_ context.Context, r *lgmonv1.TeamAdminRequest) ([]byte, error) {
		if r.Operation == "too_large" {
			return make([]byte, TeamResponseLimit+1), nil
		}
		if r.Params["panel"] != panelID {
			return nil, fmt.Errorf("long configured identifier was lost")
		}
		return data, nil
	})
	stream, err := f.admin.TeamAdmin(f.ctx, &lgmonv1.TeamAdminRequest{Operation: "list_teams", Params: map[string]string{"panel": panelID}})
	if err != nil {
		t.Fatal(err)
	}
	var received bytes.Buffer
	frames := 0
	for {
		chunk, err := stream.Recv()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		if len(chunk.BodyJson) > 256*1024 {
			t.Fatal("frame budget changed")
		}
		frames++
		received.Write(chunk.BodyJson)
	}
	if frames < 20 || !bytes.Equal(received.Bytes(), data) {
		t.Fatal("stream lost result data", frames, received.Len())
	}
	for _, op := range []string{"too_large", "../query"} {
		stream, err := f.admin.TeamAdmin(f.ctx, &lgmonv1.TeamAdminRequest{Operation: op})
		if err == nil {
			_, err = stream.Recv()
		}
		if err == nil {
			t.Fatal("invalid administration request accepted", op)
		}
	}
	stream, err = f.admin.TeamAdmin(f.ctx, &lgmonv1.TeamAdminRequest{Operation: "list_teams", Params: map[string]string{"panel": strings.Repeat("x", 1024*1024+1)}})
	if err == nil {
		_, err = stream.Recv()
	}
	if err == nil {
		t.Fatal("management parameter byte budget bypassed")
	}
}
