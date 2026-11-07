package config

import (
	"bytes"
	"slices"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestAddRuleNotifierDoesNotChangeAliasedUnselectedRules(t *testing.T) {
	source := []byte("rules:\n  - {id: first, notify: &recipients [old]} # selected\n  - {id: second, notify: *recipients} # unchanged\n  - {id: third, notify: *recipients} # selected alias\n")
	result, err := AddRuleNotifier(source, []string{"first", "third"}, "new")
	if err != nil {
		t.Fatal(err)
	}
	var parsed RulesFile
	if err := yaml.Unmarshal(result, &parsed); err != nil {
		t.Fatal(err)
	}
	for i, want := range [][]string{{"old", "new"}, {"old"}, {"old", "new"}} {
		if !slices.Equal(parsed.Rules[i].Notify, want) {
			t.Fatalf("rule %d: %v", i, parsed.Rules[i].Notify)
		}
	}
	if !bytes.Contains(result, []byte("# unchanged")) {
		t.Fatal("lost comment")
	}
	again, err := AddRuleNotifier(result, []string{"first", "third"}, "new")
	if err != nil || !bytes.Equal(result, again) {
		t.Fatalf("not idempotent: %s %v", again, err)
	}
}
