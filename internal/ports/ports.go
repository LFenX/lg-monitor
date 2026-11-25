// Copyright (c) 2026 LFen (https://lfen.ink) — lg-monitor

// Package ports keeps lgmon reachable when Windows hands its ports to something else. WSL, Docker and Hyper-V
// reserve blocks of a hundred ports that differ after every boot, and binding one of them is refused although
// nothing listens there.
package ports

import (
	"context"
	"fmt"
	"net"
	"strconv"
	"strings"
)

// The alternatives are fixed, so whoever opens lgmon finds it without reading its private data directory.
var offsets = []int{10000, 20000, 30000}

type ListenFunc func(ctx context.Context, address string) (net.Listener, error)

// Fallbacks are the addresses tried, in order, when the port of address is reserved.
func Fallbacks(address string) []string {
	host, text, err := net.SplitHostPort(address)
	if err != nil {
		return nil
	}
	port, err := strconv.Atoi(text)
	if err != nil || port <= 0 {
		return nil
	}
	var result []string
	for _, offset := range offsets {
		if port+offset < 65536 {
			result = append(result, net.JoinHostPort(host, strconv.Itoa(port+offset)))
		}
	}
	return result
}

// Listen binds address, or the first free alternative when the system has reserved its port. A port another
// program listens on is still an error: that program may be another lgmon.
func Listen(ctx context.Context, address string, listen ListenFunc) (net.Listener, error) {
	return listenWith(ctx, address, listen, Reserved)
}

func listenWith(ctx context.Context, address string, listen ListenFunc, reserved func(error) bool) (net.Listener, error) {
	listener, err := listen(ctx, address)
	if err == nil || !reserved(err) {
		return listener, err
	}
	for _, candidate := range Fallbacks(address) {
		if listener, candidateErr := listen(ctx, candidate); candidateErr == nil {
			return listener, nil
		}
	}
	return nil, fmt.Errorf("端口被 Windows 保留（通常是 WSL、Docker 或 Hyper-V 的端口段），备用端口也不可用：%w", err)
}

// Moved is a port lgmon could not have this time and the one it uses instead.
type Moved struct {
	Configured int
	Actual     int
}

// Compare reports whether a listener ended up on another port than configured.
func Compare(configured string, actual net.Addr) (Moved, bool) {
	if actual == nil {
		return Moved{}, false
	}
	want, got := Number(configured), Number(actual.String())
	if want == 0 || got == 0 || want == got {
		return Moved{}, false
	}
	return Moved{Configured: want, Actual: got}, true
}

// Number is the port of a listen address, or 0.
func Number(address string) int {
	_, text, err := net.SplitHostPort(address)
	if err != nil {
		return 0
	}
	port, _ := strconv.Atoi(text)
	return port
}

// Fix is what an administrator runs to get the ports back at once; it interrupts WSL and Docker for a moment.
func Fix(tcp []int) []string {
	lines := []string{"net stop winnat"}
	for _, port := range tcp {
		lines = append(lines, "netsh "+strings.Join(addArgs("tcp", port), " "))
	}
	return append(lines, "net start winnat", "Restart-Service lgmon")
}

func addArgs(protocol string, port int) []string { return rangeArgs("add", protocol, port) }

func rangeArgs(action, protocol string, port int) []string {
	return []string{"int", "ipv4", action, "excludedportrange", "protocol=" + protocol, "startport=" + strconv.Itoa(port), "numberofports=1", "store=persistent"}
}
