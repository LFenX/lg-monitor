package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/LFenX/lg-monitor/internal/transport"
	lgmonv1 "github.com/LFenX/lg-monitor/proto/lgmon/v1"
	"github.com/spf13/cobra"
	"google.golang.org/grpc"
)

type fakePairAdmin struct {
	ttl     int64
	revoked string
	items   []*lgmonv1.CollectorIdentity
}

func (*fakePairAdmin) TestFeishu(context.Context, *lgmonv1.ServiceTestRequest, ...grpc.CallOption) (*lgmonv1.ServiceTestResponse, error) {
	panic("pair commands must not send test messages")
}

func (*fakePairAdmin) Authority(context.Context, *lgmonv1.AuthorityRequest, ...grpc.CallOption) (*lgmonv1.AuthorityResponse, error) {
	panic("CLI must use the pairing coordinator")
}
func (*fakePairAdmin) Join(context.Context, *lgmonv1.JoinRequest, ...grpc.CallOption) (*lgmonv1.JoinResponse, error) {
	panic("CLI must use the pairing coordinator")
}
func (f *fakePairAdmin) NewCode(_ context.Context, in *lgmonv1.NewCodeRequest, _ ...grpc.CallOption) (*lgmonv1.NewCodeResponse, error) {
	f.ttl = in.TtlMillis
	return &lgmonv1.NewCodeResponse{Code: "ABCD-EFGH-JKLM", ExpiresAt: 123456789, Fingerprint: strings.Repeat("ABCD1234", 8)}, nil
}
func (f *fakePairAdmin) ListCollectors(context.Context, *lgmonv1.ListCollectorsRequest, ...grpc.CallOption) (*lgmonv1.ListCollectorsResponse, error) {
	return &lgmonv1.ListCollectorsResponse{Collectors: f.items}, nil
}
func (f *fakePairAdmin) RevokeCollector(_ context.Context, in *lgmonv1.RevokeCollectorRequest, _ ...grpc.CallOption) (*lgmonv1.RevokeCollectorResponse, error) {
	f.revoked = in.Id
	return &lgmonv1.RevokeCollectorResponse{}, nil
}

func TestPairCommandsPreserveHomeAndConfirmationBranches(t *testing.T) {
	home := t.TempDir()
	admin := &fakePairAdmin{}
	fingerprint := strings.Repeat("ABCD1234", 8)
	closed, joinCalls := 0, 0
	deps := pairDependencies{
		Admin: func(_ context.Context, got string) (lgmonv1.PairingClient, func() error, error) {
			if got != home {
				t.Fatal("wrong admin home")
			}
			return admin, func() error { closed++; return nil }, nil
		},
		Join: func(_ context.Context, got, address, code, pin string, confirm func(string) (bool, error)) (string, error) {
			joinCalls++
			if got != home || address != "monitor.test:7443" || code != "ABCD-EFGH-JKLM" {
				t.Fatal("wrong join arguments")
			}
			if pin == "" {
				if confirm == nil {
					return fingerprint, &transport.FingerprintRequired{Fingerprint: fingerprint}
				}
				ok, err := confirm(fingerprint)
				if err != nil {
					return fingerprint, err
				}
				if !ok {
					return fingerprint, transport.ErrFingerprint
				}
			}
			if pin != "" {
				if err := transport.VerifyFingerprint(fingerprint, pin); err != nil {
					return fingerprint, err
				}
			}
			return fingerprint, nil
		},
	}
	run := func(input string, args ...string) (string, error) {
		root := &cobra.Command{Use: "test", SilenceErrors: true, SilenceUsage: true}
		root.PersistentFlags().String("home", home, "fake home")
		root.AddCommand(newPairCommand(deps), newCollectorsCommand(deps))
		var output bytes.Buffer
		root.SetOut(&output)
		root.SetErr(&output)
		root.SetIn(strings.NewReader(input))
		root.SetArgs(args)
		err := root.Execute()
		return output.String(), err
	}
	output, err := run("", "pair", "new", "--ttl", "2m", "--json")
	if err != nil {
		t.Fatal(err)
	}
	var codeResult pairOutput
	if err := json.Unmarshal([]byte(output), &codeResult); err != nil || codeResult.Status != "ok" || codeResult.FingerprintShort != "ABCD-1234-ABCD-1234" || admin.ttl != 120000 || closed != 1 {
		t.Fatalf("new code: %s %v", output, err)
	}
	if _, err := run("", "pair", "new", "--ttl", "0s"); err == nil {
		t.Fatal("invalid TTL accepted")
	}
	args := []string{"pair", "join", "--monitor", "monitor.test:7443", "--code", "ABCD-EFGH-JKLM"}
	output, err = run("", append(args, "--json")...)
	if err != nil || !strings.Contains(output, `"status":"action_required"`) {
		t.Fatalf("confirmation required: %s %v", output, err)
	}
	output, err = run("", append(args, "--fingerprint", fingerprint, "--json")...)
	if err != nil || !strings.Contains(output, `"status":"ok"`) || strings.Contains(output, "ABCD-EFGH-JKLM") {
		t.Fatalf("confirmed: %s %v", output, err)
	}
	output, err = run("", append(args, "--fingerprint", "00000000", "--json")...)
	var reported *reportedError
	if !errors.As(err, &reported) || !strings.Contains(output, `"status":"error"`) {
		t.Fatalf("mismatched pin: %s %v", output, err)
	}
	if _, err := run("abcd:1234:abcd:1234\n", args...); err != nil {
		t.Fatalf("interactive confirmation: %v", err)
	}
	if _, err := run("00000000\n", args...); !errors.Is(err, transport.ErrFingerprint) {
		t.Fatalf("interactive rejection: %v", err)
	}
	if joinCalls != 5 {
		t.Fatalf("join calls: %d", joinCalls)
	}
	output, err = run("", "collectors", "list", "--json")
	if err != nil || !strings.Contains(output, `"collectors":[]`) {
		t.Fatalf("empty stable list: %s %v", output, err)
	}
	cacheRate := 25000000.0
	admin.items = []*lgmonv1.CollectorIdentity{{Id: "fake-id", Hostname: "FakeHost", Serial: "123", IssuedAt: 1000, Revoked: true, Cache: &lgmonv1.CollectorCache{Bytes: 1000000, MaxBytes: 500000000, BytesPerHour: &cacheRate},
		Config: &lgmonv1.CollectorConfigState{State: "outdated", Version: "fake-target-version", Message: "目标 blog 使用 workDir，请升级采集器"}}}
	output, err = run("", "collectors", "list", "--json")
	if err != nil || !strings.Contains(output, `"revoked":true`) {
		t.Fatalf("listing: %s %v", output, err)
	}
	var listed pairOutput
	if err := json.Unmarshal([]byte(output), &listed); err != nil || listed.Collectors == nil || len(*listed.Collectors) != 1 ||
		(*listed.Collectors)[0].Config == nil || (*listed.Collectors)[0].Config.State != "outdated" ||
		(*listed.Collectors)[0].Config.Version != "fake-target-version" || (*listed.Collectors)[0].Config.Message != admin.items[0].Config.Message ||
		(*listed.Collectors)[0].Cache == nil || (*listed.Collectors)[0].Cache.MaxBytes != 500000000 ||
		(*listed.Collectors)[0].Cache.BytesPerHour == nil || *(*listed.Collectors)[0].Cache.BytesPerHour != cacheRate ||
		(*listed.Collectors)[0].Cache.ProjectedHours == nil || *(*listed.Collectors)[0].Cache.ProjectedHours >= 24 {
		t.Fatalf("configuration progress lost in CLI JSON: %s %v", output, err)
	}
	output, err = run("", "collectors", "revoke", "fake-id", "--json")
	if err != nil || admin.revoked != "fake-id" || !strings.Contains(output, `"collectorId":"fake-id"`) {
		t.Fatalf("revoke: %s %v", output, err)
	}
}
