package transport

import (
	"context"
	"net"
	"sync/atomic"
	"testing"
	"time"
)

func TestLANDiscoveryAnswersOnlyWhileVisible(t *testing.T) {
	server, err := net.ListenPacket("udp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	var visible atomic.Bool
	visible.Store(true)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- ServeLANDiscovery(ctx, server, func() (LANBeacon, bool) {
			return LANBeacon{Name: "虚构电脑", Port: 7445, Fingerprint: "ABCDEF0123456789"}, visible.Load()
		})
	}()
	ask := func() []LANPeer {
		client, err := net.ListenPacket("udp4", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		defer client.Close()
		peers, err := DiscoverLAN(client, []net.Addr{server.LocalAddr()}, time.Now().Add(300*time.Millisecond))
		if err != nil {
			t.Fatal(err)
		}
		return peers
	}
	peers := ask()
	if len(peers) != 1 || peers[0].Name != "虚构电脑" || peers[0].Address != "https://127.0.0.1:7445" || peers[0].Fingerprint != "ABCDEF0123456789" {
		t.Fatalf("peers=%+v", peers)
	}
	// Garbage and foreign packets are ignored rather than answered.
	client, _ := net.ListenPacket("udp4", "127.0.0.1:0")
	_, _ = client.WriteTo([]byte(`{"lgmon":"other","kind":"discover"}`), server.LocalAddr())
	_ = client.SetReadDeadline(time.Now().Add(200 * time.Millisecond))
	if n, _, err := client.ReadFrom(make([]byte, 100)); err == nil {
		t.Fatalf("answered a foreign packet with %d bytes", n)
	}
	client.Close()
	visible.Store(false)
	if peers := ask(); len(peers) != 0 {
		t.Fatalf("hidden computer answered: %+v", peers)
	}
	cancel()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}
