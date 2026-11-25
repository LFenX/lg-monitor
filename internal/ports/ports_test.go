// Copyright (c) 2026 LFen (https://lfen.ink) — lg-monitor

package ports

import (
	"context"
	"errors"
	"fmt"
	"net"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

type fakeListener struct {
	net.Listener
	address string
}

func (l fakeListener) Addr() net.Addr {
	address, _ := net.ResolveTCPAddr("tcp", l.address)
	return address
}

func TestListenMovesOnlyOffReservedPorts(t *testing.T) {
	errReserved, errInUse := errors.New("reserved"), errors.New("in use")
	reserved := func(err error) bool { return errors.Is(err, errReserved) }
	for name, test := range map[string]struct {
		failures map[string]error
		want     string
		err      error
	}{
		"free":                {nil, "127.0.0.1:7444", nil},
		"reserved":            {map[string]error{"127.0.0.1:7444": errReserved}, "127.0.0.1:17444", nil},
		"reserved twice":      {map[string]error{"127.0.0.1:7444": errReserved, "127.0.0.1:17444": errInUse}, "127.0.0.1:27444", nil},
		"another lgmon":       {map[string]error{"127.0.0.1:7444": errInUse}, "", errInUse},
		"nothing else either": {map[string]error{"127.0.0.1:7444": errReserved, "127.0.0.1:17444": errReserved, "127.0.0.1:27444": errInUse, "127.0.0.1:37444": errInUse}, "", errReserved},
	} {
		listener, err := listenWith(context.Background(), "127.0.0.1:7444", func(_ context.Context, address string) (net.Listener, error) {
			if err := test.failures[address]; err != nil {
				return nil, err
			}
			return fakeListener{address: address}, nil
		}, reserved)
		if !errors.Is(err, test.err) || (test.err == nil && listener.Addr().String() != test.want) {
			t.Fatalf("%s: %v, %v", name, listener, err)
		}
		if test.err == nil {
			moved, ok := Compare("127.0.0.1:7444", listener.Addr())
			if ok != (test.want != "127.0.0.1:7444") || (ok && moved.Configured != 7444) {
				t.Fatalf("%s: moved %+v %v", name, moved, ok)
			}
		}
	}
}

func TestFallbacksStayOnTheSameHostWithinRange(t *testing.T) {
	if got := Fallbacks("0.0.0.0:7443"); !slices.Equal(got, []string{"0.0.0.0:17443", "0.0.0.0:27443", "0.0.0.0:37443"}) {
		t.Fatalf("got %v", got)
	}
	if got := Fallbacks(":50000"); !slices.Equal(got, []string{":60000"}) {
		t.Fatalf("got %v", got)
	}
	for _, address := range []string{"127.0.0.1:0", "nonsense", "127.0.0.1:65000"} {
		if got := Fallbacks(address); len(got) != 0 {
			t.Fatalf("%s: %v", address, got)
		}
	}
}

// fakeNetsh keeps exclusions like netsh does; taken ports are refused as if WinNAT held them.
type fakeNetsh struct {
	excluded map[string][]int
	taken    []int
	ignored  []int
	calls    []string
}

func (f *fakeNetsh) run(_ context.Context, args ...string) (string, error) {
	f.calls = append(f.calls, strings.Join(args, " "))
	var protocol string
	var port int
	for _, arg := range args {
		if value, ok := strings.CutPrefix(arg, "protocol="); ok {
			protocol = value
		}
		if value, ok := strings.CutPrefix(arg, "startport="); ok {
			_, _ = fmt.Sscan(value, &port)
		}
	}
	switch args[2] {
	case "show":
		out := "\r\n协议 tcp 端口排除范围\r\n\r\n开始端口    结束端口\r\n----------    --------\r\n"
		for _, item := range f.excluded[protocol] {
			out += fmt.Sprintf("%10d %13d     *\r\n", item, item)
		}
		return out + "\r\n* - 管理的端口排除。\r\n", nil
	case "add":
		if slices.Contains(f.taken, port) {
			return "The process cannot access the file because it is being used by another process.", errors.New("exit status 1")
		}
		if slices.Contains(f.ignored, port) {
			return "Usage: add excludedportrange", nil
		}
		f.excluded[protocol] = append(f.excluded[protocol], port)
	case "delete":
		f.excluded[protocol] = slices.DeleteFunc(f.excluded[protocol], func(item int) bool { return item == port })
	}
	return "", nil
}

func TestKeeperRegistersOnceRetriesRefusalsAndReleasesItsOwn(t *testing.T) {
	ctx := context.Background()
	// 7445 was registered by the owner; lgmon must neither repeat nor remove it.
	fake := &fakeNetsh{excluded: map[string][]int{"tcp": {7445}}, taken: []int{7443}}
	keeper := Keeper{Path: filepath.Join(t.TempDir(), "data", "kept-ports.json"), Run: fake.run}
	if err := keeper.Keep(ctx, []int{7443, 7444, 7445, 80, 0}, []int{7446}); err == nil || !strings.Contains(err.Error(), "7443") {
		t.Fatalf("the refused port should be reported: %v", err)
	}
	if !slices.Equal(fake.excluded["tcp"], []int{7445, 7444}) || !slices.Equal(fake.excluded["udp"], []int{7446}) {
		t.Fatalf("registered %v", fake.excluded)
	}
	fake.taken = nil
	if err := keeper.Keep(ctx, []int{7443, 7444, 7445}, []int{7446}); err != nil {
		t.Fatal(err)
	}
	fake.calls = nil
	if err := keeper.Keep(ctx, []int{7443, 7444, 7445}, []int{7446}); err != nil || len(fake.calls) != 2 {
		t.Fatalf("a settled start only reads: %v %v", fake.calls, err)
	}
	if err := keeper.Release(ctx); err != nil || !slices.Equal(fake.excluded["tcp"], []int{7445}) || len(fake.excluded["udp"]) != 0 {
		t.Fatalf("after release %v, %v", fake.excluded, err)
	}
	fake.calls = nil
	if err := keeper.Release(ctx); err != nil || len(fake.calls) != 0 {
		t.Fatalf("nothing is left to release: %v %v", fake.calls, err)
	}
}

func TestKeeperDoesNotTrustASuccessThatRegisteredNothing(t *testing.T) {
	fake := &fakeNetsh{excluded: map[string][]int{}, ignored: []int{7444}}
	keeper := Keeper{Path: filepath.Join(t.TempDir(), "data", "kept-ports.json"), Run: fake.run}
	if err := keeper.Keep(context.Background(), []int{7443, 7444}, nil); err == nil || !strings.Contains(err.Error(), "7444") {
		t.Fatalf("got %v", err)
	}
	if record := keeper.load(); !slices.Equal(record.TCP, []int{7443}) {
		t.Fatalf("recorded %+v", record)
	}
}

func TestFixRegistersWhileTheReservationsAreReleased(t *testing.T) {
	lines := Fix([]int{7443, 7444})
	if len(lines) != 5 || lines[0] != "net stop winnat" || lines[3] != "net start winnat" || !strings.Contains(lines[1], "startport=7443") || !strings.Contains(lines[2], "store=persistent") {
		t.Fatalf("%v", lines)
	}
}
