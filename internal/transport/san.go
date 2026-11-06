package transport

import (
	"context"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"time"
)

type NativeSANs struct{}

func (NativeSANs) Discover(ctx context.Context) (SANs, error) {
	hostname, err := os.Hostname()
	if err != nil {
		return SANs{}, fmt.Errorf("读取证书主机名失败：%w", err)
	}
	addresses, err := net.InterfaceAddrs()
	if err != nil {
		return SANs{}, fmt.Errorf("读取证书本机 IP 失败：%w", err)
	}
	result := SANs{Names: []string{"localhost", hostname}, IPs: []net.IP{net.ParseIP("127.0.0.1"), net.ParseIP("::1")}}
	for _, address := range addresses {
		ip, _, err := net.ParseCIDR(address.String())
		if err == nil {
			result.IPs = appendUniqueIP(result.IPs, ip)
		}
	}
	commandPath, err := exec.LookPath("tailscale")
	if err != nil {
		commandPath = filepath.Join(os.Getenv("ProgramFiles"), "Tailscale", "tailscale.exe")
		if _, err := os.Stat(commandPath); err != nil {
			return result, nil
		}
	}
	queryCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	// Failure to discover Tailscale must not prevent local monitoring.
	output, err := exec.CommandContext(queryCtx, commandPath, "ip").Output()
	if err == nil {
		for _, field := range strings.Fields(string(output)) {
			if ip := net.ParseIP(field); ip != nil {
				result.IPs = appendUniqueIP(result.IPs, ip)
			}
		}
	}
	return result, nil
}

func appendUniqueIP(addresses []net.IP, ip net.IP) []net.IP {
	if !slices.ContainsFunc(addresses, func(existing net.IP) bool { return existing.Equal(ip) }) {
		return append(addresses, ip)
	}
	return addresses
}
