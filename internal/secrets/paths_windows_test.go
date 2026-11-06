package secrets

import "testing"

func TestProtectedRootValidationOnlyUsesPathStrings(t *testing.T) {
	for _, path := range []string{`C:\`, `\\invalid-lgmon-test\share`, `\\invalid-lgmon-test\share\`, `\\?\C:\`, `relative\home`} {
		if _, err := protectedRoot(path); err == nil {
			t.Fatalf("root or relative path accepted: %s", path)
		}
	}
	if path, err := protectedRoot(`Z:\own-home`); err != nil || path != `Z:\own-home` {
		t.Fatalf("valid child path refused: %s %v", path, err)
	}
}
