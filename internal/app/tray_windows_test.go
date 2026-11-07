//go:build windows

package app

import (
	"bytes"
	"errors"
	"strings"
	"testing"
)

func TestUserTrayDPAPIRoundTripDoesNotStorePlaintext(t *testing.T) {
	p := NewTrayProtector()
	plain := []byte("fake-tray-credential-for-current-account")
	cipher, err := p.Encrypt(plain)
	if err != nil || bytes.Contains(cipher, plain) {
		t.Fatalf("DPAPI protection failed: %v", err)
	}
	result, err := p.Decrypt(cipher)
	if err != nil || !bytes.Equal(result, plain) {
		t.Fatalf("DPAPI round trip failed: %v", err)
	}
}

type fakeTrayEnvironment struct {
	service                  bool
	session                  uint32
	serviceErr, sessionErr   error
	serviceRead, sessionRead int
}

func (e *fakeTrayEnvironment) IsService() (bool, error) {
	e.serviceRead++
	return e.service, e.serviceErr
}
func (e *fakeTrayEnvironment) SessionID() (uint32, error) {
	e.sessionRead++
	return e.session, e.sessionErr
}

func TestTrayEnvironmentKeepsServicesAndSessionZeroOutOfGUI(t *testing.T) {
	for _, test := range []struct {
		name        string
		env         fakeTrayEnvironment
		want        string
		sessionRead int
	}{
		{"service", fakeTrayEnvironment{service: true, session: 1}, "Windows 服务不显示托盘", 0},
		{"session-zero", fakeTrayEnvironment{session: 0}, "会话 0 不显示托盘", 1},
		{"service-check-failed", fakeTrayEnvironment{serviceErr: errors.New("fake service lookup")}, "检查托盘运行上下文失败", 0},
		{"session-check-failed", fakeTrayEnvironment{sessionErr: errors.New("fake session lookup")}, "检查托盘用户会话失败", 1},
		{"interactive", fakeTrayEnvironment{session: 1}, "", 1},
	} {
		t.Run(test.name, func(t *testing.T) {
			err := trayInteractive(&test.env)
			if test.want == "" && err != nil || test.want != "" && (err == nil || !strings.Contains(err.Error(), test.want)) {
				t.Fatalf("tray environment: %v", err)
			}
			if test.env.serviceRead != 1 || test.env.sessionRead != test.sessionRead {
				t.Fatalf("environment reads: service=%d session=%d", test.env.serviceRead, test.env.sessionRead)
			}
		})
	}
}
