// Copyright (c) 2026 LFen (https://lfen.ink) — lg-monitor

// Package update finds, downloads and checks new lgmon releases for the launcher, and compares versions.
package update

import (
	"strconv"
	"strings"
)

// Newer reports whether version a is newer than b, comparing dotted numbers and ignoring a leading v and any
// suffix after a dash. A version that is not dotted numbers (such as dev) is never newer and never older.
func Newer(a, b string) bool {
	parse := func(value string) []int {
		value = strings.TrimPrefix(strings.TrimSpace(value), "v")
		value, _, _ = strings.Cut(value, "-")
		var parts []int
		for _, part := range strings.Split(value, ".") {
			number, err := strconv.Atoi(part)
			if err != nil {
				return nil
			}
			parts = append(parts, number)
		}
		return parts
	}
	left, right := parse(a), parse(b)
	if left == nil || right == nil {
		return false
	}
	for index := 0; index < max(len(left), len(right)); index++ {
		var x, y int
		if index < len(left) {
			x = left[index]
		}
		if index < len(right) {
			y = right[index]
		}
		if x != y {
			return x > y
		}
	}
	return false
}
