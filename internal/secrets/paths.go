// Copyright (c) 2026 LFen (https://lfen.ink) — lg-monitor

package secrets

type PathPolicy interface {
	Check(string) error
	ProtectTree(string, bool) error
}
