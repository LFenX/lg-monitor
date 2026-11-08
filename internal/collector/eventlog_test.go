package collector

import (
	"context"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"testing"
	"unicode/utf8"
)

func eventXML(channel, provider string, id int, data string) string {
	return fmt.Sprintf(`<Event xmlns="http://schemas.microsoft.com/win/2004/08/events/event"><System><Provider Name="%s"/><EventID>%d</EventID><Level>2</Level><TimeCreated SystemTime="2026-11-09T00:00:00.1234567Z"/><EventRecordID>42</EventRecordID><Channel>%s</Channel></System><EventData>%s</EventData></Event>`, provider, id, channel, data)
}

func TestEventLogXMLExtractsSubjectsAndFallsBackToEventData(t *testing.T) {
	for _, test := range []struct {
		channel, provider string
		id                int
		data, subject     string
	}{
		{"Application", "Application Error", 1000, `<Data Name="AppName">C:\fake\worker.exe</Data><Data>fake crash</Data>`, "worker.exe"},
		{"Application", "Application Hang", 1002, `<Data>fake-hang.exe</Data><Data>fake hang</Data>`, "fake-hang.exe"},
		{"System", "Service Control Manager", 7031, `<Data Name="param1">Fake Service</Data><Data>1</Data>`, "Fake Service"},
		{"System", "Service Control Manager", 7034, `<Data Name="ServiceName">fake-service</Data>`, "fake-service"},
		{"System", "EventLog", 6008, `<Data>fake shutdown</Data>`, ""},
		{"System", "Microsoft-Windows-Kernel-Power", 41, `<Data>fake power</Data>`, ""},
		{"Application", "fake-unregistered", 1000, `<Data>private.exe</Data><Data>own test</Data>`, ""},
	} {
		t.Run(fmt.Sprintf("%s-%d", test.provider, test.id), func(t *testing.T) {
			row, err := parseEventLogXML(test.channel, eventXML(test.channel, test.provider, test.id, test.data), "")
			if err != nil || row.Subject != test.subject || row.Message == "" || row.TS != 1794182400123 || row.RecordID != 42 || row.Level != 2 {
				t.Fatalf("event facts: %+v %v", row, err)
			}
		})
	}
	row, err := parseEventLogXML("Application", eventXML("Application", "fake-provider", 100, `<Data>fallback</Data>`), "formatted message")
	if err != nil || row.Message != "formatted message" {
		t.Fatalf("formatted message lost: %+v %v", row, err)
	}
}

func TestEventLogMessageTruncatesAtUTF8BoundaryAndRejectsOtherChannels(t *testing.T) {
	message := strings.Repeat("x", 2047) + "中文"
	row, err := parseEventLogXML("Application", eventXML("Application", "fake-provider", 1, `<Data>`+message+`</Data>`), "")
	if err != nil || len(row.Message) != 2047 || !utf8.ValidString(row.Message) {
		t.Fatalf("UTF-8 byte boundary: len=%d err=%v", len(row.Message), err)
	}
	for _, invalid := range []string{
		eventXML("Security", "fake-provider", 1, ""),
		strings.Replace(eventXML("Application", "fake-provider", 1, ""), "2026-11-09T00:00:00.1234567Z", "invalid", 1),
		`<Event>`,
	} {
		if _, err := parseEventLogXML("Application", invalid, ""); err == nil {
			t.Fatal("invalid event accepted")
		}
	}
}

type eventLogBackendCall struct {
	channel, query, bookmark string
	oldest                   bool
}

type fakeEventLogBackend struct {
	calls []eventLogBackendCall
	err   error
}

func (b *fakeEventLogBackend) Anchor(context.Context, string) (string, error) {
	return "fake-anchor", b.err
}

func (b *fakeEventLogBackend) Open(_ context.Context, channel, query, bookmark string, oldest bool) (EventLogReader, error) {
	b.calls = append(b.calls, eventLogBackendCall{channel, query, bookmark, oldest})
	if len(b.calls) == 1 {
		return nil, b.err
	}
	return nil, nil
}

func TestEventLogSourceRestrictsChannelsAndFallsBackWithoutDiscardingBookmark(t *testing.T) {
	backend := &fakeEventLogBackend{err: ErrEventLogQueryComplex}
	source := eventLogSource{backend: backend, logger: slog.New(slog.NewTextHandler(io.Discard, nil))}
	request := EventLogQuery{Channel: "Application", Bookmark: "fake-bookmark", Filters: []EventLogFilter{{Provider: "fake-provider", IDs: []uint16{1, 2}}}}
	if _, err := source.Subscribe(context.Background(), request); err != nil || len(backend.calls) != 2 {
		t.Fatalf("fallback: %+v %v", backend.calls, err)
	}
	if !strings.Contains(backend.calls[0].query, "EventID=1") || strings.Contains(backend.calls[1].query, "EventID=") || !strings.Contains(backend.calls[1].query, "fake-provider") || backend.calls[1].bookmark != request.Bookmark || backend.calls[1].oldest {
		t.Fatalf("fallback changed resume or provider: %+v", backend.calls)
	}
	request.Channel = "Security"
	if _, err := source.Subscribe(context.Background(), request); err == nil || len(backend.calls) != 2 {
		t.Fatal("Security channel reached the system interface")
	}
	if _, err := source.Anchor(context.Background(), "Security"); err == nil {
		t.Fatal("Security anchor reached the system interface")
	}
	request.Channel, backend.err, backend.calls = "System", ErrEventLogBookmarkStale, nil
	if _, err := source.Subscribe(context.Background(), request); !errors.Is(err, ErrEventLogBookmarkStale) || len(backend.calls) != 1 {
		t.Fatal("stale bookmark was silently restarted without a gap", err)
	}
	backend.err, backend.calls = nil, nil
	request.Bookmark = EventLogEmptyBookmark
	if _, err := source.Subscribe(context.Background(), request); err != nil || len(backend.calls) != 1 || !backend.calls[0].oldest || backend.calls[0].bookmark != "" {
		t.Fatalf("empty-log anchor skipped events written after initial setup: %+v %v", backend.calls, err)
	}
}

func TestEventLogQuerySplitsLargeFiltersAndEscapesProviderData(t *testing.T) {
	var ids []uint16
	for id := uint16(0); id < 50; id++ {
		ids = append(ids, id)
	}
	query := eventLogQuery("Application", []EventLogFilter{{Provider: "fake & provider", IDs: ids}, {Provider: "fake & provider", IDs: []uint16{1}}}, false)
	if strings.Contains(query, "&#39;") || strings.Contains(query, "&#34;") {
		t.Fatal("Windows structured queries reject numeric quote references")
	}
	var parsed struct {
		Queries []struct {
			Path    string `xml:"Path,attr"`
			Selects []struct {
				Path  string `xml:"Path,attr"`
				Value string `xml:",chardata"`
			} `xml:"Select"`
		} `xml:"Query"`
	}
	if err := xml.Unmarshal([]byte(query), &parsed); err != nil || len(parsed.Queries) != 1 || len(parsed.Queries[0].Selects) != 4 {
		t.Fatalf("structured query: %s %v", query, err)
	}
	for _, selectQuery := range parsed.Queries[0].Selects {
		if selectQuery.Path != "Application" || strings.Count(selectQuery.Value, "EventID=") > 16 || !strings.Contains(selectQuery.Value, "fake & provider") {
			t.Fatalf("unsafe or oversized query: %+v", selectQuery)
		}
	}
	if broad := eventLogQuery("Application", []EventLogFilter{{Provider: `fake'"<Select Path="Security">`}}, false); broad != "*" {
		t.Fatalf("provider data escaped into XPath: %s", broad)
	}
}
