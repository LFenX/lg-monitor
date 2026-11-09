//go:build !windows

package app

func applyCollectorResidentBudget() (func() error, error) { return func() error { return nil }, nil }
