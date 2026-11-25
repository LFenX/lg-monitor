// Copyright (c) 2026 LFen (https://lfen.ink) — lg-monitor

package osinfo

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"
)

// MachineKey names this computer for one use without revealing the system's own identifier: the same
// computer, purpose and instance always give the same key, and it survives reinstalling or losing data files.
// It is empty when the system has no usable identifier.
func MachineKey(purpose, instance string) string {
	return machineKey(machineID(), purpose, instance)
}

func machineKey(id, purpose, instance string) string {
	id = strings.ToLower(strings.TrimSpace(id))
	digits := strings.ReplaceAll(id, "-", "")
	// Images are shipped with an empty or "uninitialized" ID, which many computers would share.
	if raw, err := hex.DecodeString(digits); err != nil || len(raw) != 16 || strings.Trim(digits, "0") == "" {
		return ""
	}
	sum := sha256.Sum256([]byte("lgmon|" + purpose + "|" + digits + "|" + instance))
	return hex.EncodeToString(sum[:16])
}
