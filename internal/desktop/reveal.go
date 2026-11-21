// Copyright (c) 2026 LFen (https://lfen.ink) — lg-monitor

package desktop

import (
	"regexp"
	"strings"
)

var revealNonce = regexp.MustCompile(`^[0-9a-f]{4}$`)

// Invisible characters, written as code points so the source shows them.
const (
	wordJoiner rune = 0x2060
	zeroBit    rune = 0x200b
	oneBit     rune = 0x200c
)

// RevealMarker is what an open lgmon page appends to its title for a few seconds when asked to come forward:
// a word joiner and the nonce's 16 bits as zero-width spaces (0) and non-joiners (1). Browsers keep these in the
// window title, so the window can be found without the person seeing anything. web/src/reveal.ts must match.
func RevealMarker(nonce string) string {
	if !revealNonce.MatchString(nonce) {
		return ""
	}
	var b strings.Builder
	b.WriteRune(wordJoiner)
	for _, digit := range nonce {
		value := strings.IndexRune("0123456789abcdef", digit)
		for bit := 3; bit >= 0; bit-- {
			if value>>bit&1 == 1 {
				b.WriteRune(oneBit)
			} else {
				b.WriteRune(zeroBit)
			}
		}
	}
	return b.String()
}
