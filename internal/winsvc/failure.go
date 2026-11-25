// Copyright (c) 2026 LFen (https://lfen.ink) — lg-monitor

package winsvc

import (
	"encoding/xml"
	"strings"
)

// The standard descriptor lets logged-on users query a service; RP is the right to start it.
const (
	interactiveQuery = "(A;;CCLCSWLOCRRC;;;IU)"
	interactiveStart = "(A;;CCLCSWRPLOCRRC;;;IU)"
)

// grantInteractiveStart adds the start right to the standard entry; a descriptor someone changed stays as it is.
func grantInteractiveStart(sddl string) (string, bool) {
	if !strings.Contains(sddl, interactiveQuery) {
		return sddl, false
	}
	return strings.Replace(sddl, interactiveQuery, interactiveStart, 1), true
}

const failurePrefix = "lgmon service failed: "

// failureMessage reads the reason out of an event log entry of the service.
func failureMessage(event []byte) string {
	var entry struct {
		Data []string `xml:"EventData>Data"`
	}
	if xml.Unmarshal(event, &entry) != nil || len(entry.Data) == 0 {
		return ""
	}
	message := strings.TrimSpace(entry.Data[0])
	return strings.TrimSpace(strings.TrimPrefix(message, failurePrefix))
}
