//go:build !windows

package collector

import "os"

func publishIdentity(temporary, destination string) error { return os.Link(temporary, destination) }

func identitySharingViolation(error) bool { return false }
