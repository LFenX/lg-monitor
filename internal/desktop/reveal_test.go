package desktop

import "testing"

func TestRevealMarkerEncodesTheNonceInvisibly(t *testing.T) {
	// The same vector is checked in web/src/reveal.test.ts.
	if got, want := RevealMarker("a5f0"), "⁠‌​‌​​‌​‌‌‌‌‌​​​​"; got != want {
		t.Fatalf("RevealMarker(a5f0) = %q, want %q", got, want)
	}
	for _, bad := range []string{"", "A5F0", "a5f", "a5f00", "zzzz"} {
		if RevealMarker(bad) != "" {
			t.Fatalf("accepted nonce %q", bad)
		}
	}
}
