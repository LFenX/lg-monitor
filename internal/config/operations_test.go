package config

import (
	"strings"
	"testing"
	"testing/fstest"
)

func TestRemoteOperationsDefaultValidationAndPreservedSettings(t *testing.T) {
	if err := (RemoteOperations{}).Validate(); err != nil {
		t.Fatal(err)
	}
	policy := RemoteOperations{Enabled: true, Services: []ServiceGrant{{ID: "restart-demo", Service: "FakeService"}}}
	before := []byte("# retained\nhttpListen: 127.0.0.1:7444\ncache: {maxSize: 500MB}\n")
	after, err := RemoteOperationsConfig(before, policy)
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadSettings(fstest.MapFS{"settings.yaml": {Data: after}})
	if err != nil || cfg.RemoteOperations.Version() != policy.Version() || cfg.HTTPListen != "127.0.0.1:7444" || !strings.Contains(string(after), "# retained") {
		t.Fatal(cfg, err, string(after))
	}
	for _, invalid := range []RemoteOperations{{Enabled: true}, {Services: []ServiceGrant{{ID: "x", Service: "lgmon"}}}, {Services: []ServiceGrant{{ID: "x", Service: "*"}}}, {Services: []ServiceGrant{{ID: "x", Service: "foo"}, {ID: "y", Service: "FOO"}}}, {Services: []ServiceGrant{{ID: "x", Service: "foo"}, {ID: "x", Service: "bar"}}}} {
		if err := invalid.Validate(); err == nil {
			t.Fatal("invalid local grant accepted", invalid)
		}
	}
}
