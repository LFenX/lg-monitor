package api

import (
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strconv"
	"strings"
)

type HostPolicy struct{ names map[string]bool }

func hostKey(value string) (string, error) {
	host := value
	if parsed, port, err := net.SplitHostPort(value); err == nil {
		n, err := strconv.Atoi(port)
		if err != nil || n < 0 || n > 65535 {
			return "", fmt.Errorf("主机端口无效")
		}
		host = parsed
	}
	host = strings.Trim(host, "[]")
	if address, err := netip.ParseAddr(host); err == nil {
		return address.Unmap().WithZone("").String(), nil
	}
	if host == "" || strings.ContainsAny(host, " :/@?#\\\t\r\n[]") {
		return "", fmt.Errorf("主机名无效")
	}
	return strings.ToLower(strings.TrimSuffix(host, ".")), nil
}

func NewHostPolicy(listen, publicBaseURL string, addresses []string) (*HostPolicy, error) {
	addresses = append([]string(nil), addresses...)
	policy := &HostPolicy{names: map[string]bool{"localhost": true, "127.0.0.1": true, "::1": true}}
	host, _, err := net.SplitHostPort(listen)
	if err != nil {
		return nil, fmt.Errorf("解析 HTTP 监听地址失败：%w", err)
	}
	if host != "" {
		addresses = append(addresses, host)
	}
	if publicBaseURL != "" {
		parsed, err := url.Parse(publicBaseURL)
		if err != nil || parsed.Hostname() == "" {
			return nil, fmt.Errorf("publicBaseUrl 的主机名无效")
		}
		addresses = append(addresses, parsed.Hostname())
	}
	for _, address := range addresses {
		key, err := hostKey(address)
		if err != nil {
			return nil, fmt.Errorf("登记 HTTP 允许的主机名失败：%w", err)
		}
		policy.names[key] = true
	}
	return policy, nil
}

func (p *HostPolicy) Check(r *http.Request) error {
	host, err := hostKey(r.Host)
	if err != nil || !p.names[host] {
		return fmt.Errorf("请求的 Host 不在监控器允许范围")
	}
	origins := r.Header.Values("Origin")
	if len(origins) > 1 {
		return fmt.Errorf("请求包含多个 Origin")
	}
	if len(origins) == 1 {
		origin, err := url.Parse(origins[0])
		if err != nil || (origin.Scheme != "http" && origin.Scheme != "https") || origin.User != nil || origin.Path != "" || origin.RawQuery != "" || origin.Fragment != "" {
			return fmt.Errorf("请求的 Origin 无效")
		}
		key, err := hostKey(origin.Host)
		if err != nil || !p.names[key] {
			return fmt.Errorf("请求的 Origin 不在监控器允许范围")
		}
	}
	return nil
}

func localRequest(r *http.Request) bool {
	peer, err := netip.ParseAddrPort(r.RemoteAddr)
	if err != nil || !peer.Addr().Unmap().IsLoopback() {
		return false
	}
	host, err := hostKey(r.Host)
	if err != nil {
		return false
	}
	if host != "localhost" && host != "127.0.0.1" && host != "::1" {
		return false
	}
	for name := range r.Header {
		name = strings.ToLower(name)
		if name == "forwarded" || strings.HasPrefix(name, "x-forwarded-") || name == "cf-connecting-ip" || name == "x-real-ip" || name == "true-client-ip" || name == "via" {
			return false
		}
	}
	return true
}
