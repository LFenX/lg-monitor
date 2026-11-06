package transport

import (
	"errors"
	"strings"
	"testing"
	"time"
)

func TestFingerprintFormsAndMinimumLength(t *testing.T) {
	actual := strings.Repeat("3F2A9C1B77D04E85", 4)
	if got := FingerprintShort(actual); got != "3F2A-9C1B-77D0-4E85" {
		t.Fatalf("short fingerprint: %s", got)
	}
	for _, tc := range []struct {
		value   string
		allowed bool
	}{
		{actual[:8], false}, {actual[:12], false}, {actual[:32], false},
		{actual[:16], true}, {strings.ToLower(actual[:16]), true},
		{"3f2a-9c1b-77d0-4e85", true}, {"3f2a:9c1b:77d0:4e85", true},
		{actual, true}, {strings.ToLower(actual), true},
		{strings.Repeat("0", 16), false}, {strings.Repeat("G", 16), false},
	} {
		err := VerifyFingerprint(actual, tc.value)
		if (err == nil) != tc.allowed {
			t.Fatalf("fingerprint acceptance %q: %v", tc.value, err)
		}
		if err != nil && !errors.Is(err, ErrFingerprint) {
			t.Fatalf("fingerprint error identity lost: %v", err)
		}
		if len(tc.value) == 8 && !strings.Contains(err.Error(), "16") {
			t.Fatal("short prefix has no 16-digit guidance")
		}
	}
}

func TestPairingFingerprintFormatsPinTLSAndShortPrefixNeverConsumesCode(t *testing.T) {
	for _, form := range []string{"16", "64", "grouped", "colon", "8"} {
		t.Run(form, func(t *testing.T) {
			f := newPairingFixture(t)
			code, actual := f.code(t, time.Minute), f.authority.Fingerprint()
			provided := actual[:16]
			switch form {
			case "64":
				provided = strings.ToLower(actual)
			case "grouped":
				provided = strings.ToLower(FingerprintShort(actual))
			case "colon":
				provided = strings.ReplaceAll(strings.ToLower(FingerprintShort(actual)), "-", ":")
			case "8":
				provided = actual[:8]
			}
			_, _, returned, err := f.join(t, "formatted-collector", code, provided)
			if returned != actual {
				t.Fatal("returned CA fingerprint changed")
			}
			if form == "8" {
				if !errors.Is(err, ErrFingerprint) || !strings.Contains(err.Error(), "16") {
					t.Fatalf("matching short prefix accepted: %v", err)
				}
				if _, found, err := f.repository.Find(f.ctx, "formatted-collector"); err != nil || found {
					t.Fatal("short prefix registered an identity")
				}
				f.connector.mu.Lock()
				stages := append([]bool(nil), f.connector.insecure...)
				f.connector.mu.Unlock()
				if len(stages) != 2 || !stages[1] {
					t.Fatalf("short fingerprint reached pinned submission: %v", stages)
				}
				_, _, _, err = f.join(t, "formatted-collector", code, actual)
			}
			if err != nil {
				t.Fatalf("valid fingerprint/code could not pair: %v", err)
			}
		})
	}
}
