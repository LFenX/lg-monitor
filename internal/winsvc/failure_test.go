// Copyright (c) 2026 LFen (https://lfen.ink) — lg-monitor

package winsvc

import "testing"

func TestFailureMessageReadsTheServicesOwnWords(t *testing.T) {
	event := []byte(`<Event xmlns='http://schemas.microsoft.com/win/2004/08/events/event'><System><Provider Name='lgmon'/><EventID Qualifiers='0'>2</EventID></System><EventData><Data>lgmon service failed: 监听采集连接失败：listen tcp 127.0.0.1:7443: bind: refused &amp; more</Data></EventData></Event>`)
	if got := failureMessage(event); got != "监听采集连接失败：listen tcp 127.0.0.1:7443: bind: refused & more" {
		t.Fatalf("got %q", got)
	}
	for _, empty := range []string{"", "<Event/>", "not xml"} {
		if got := failureMessage([]byte(empty)); got != "" {
			t.Fatalf("%q gave %q", empty, got)
		}
	}
}

func TestOnlyTheStandardEntryGainsTheStartRight(t *testing.T) {
	standard := "D:(A;;CCLCSWRPWPDTLOCRRC;;;SY)(A;;CCDCLCSWRPWPDTLOCRSDRCWDWO;;;BA)(A;;CCLCSWLOCRRC;;;IU)(A;;CCLCSWLOCRRC;;;SU)"
	granted, changed := grantInteractiveStart(standard)
	if !changed || granted != "D:(A;;CCLCSWRPWPDTLOCRRC;;;SY)(A;;CCDCLCSWRPWPDTLOCRSDRCWDWO;;;BA)(A;;CCLCSWRPLOCRRC;;;IU)(A;;CCLCSWLOCRRC;;;SU)" {
		t.Fatalf("%v %s", changed, granted)
	}
	if _, again := grantInteractiveStart(granted); again {
		t.Fatal("a granted descriptor needs no change")
	}
	if _, changed := grantInteractiveStart("D:(A;;GA;;;BA)"); changed {
		t.Fatal("a descriptor someone changed must stay as it is")
	}
}
