package collector

import (
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
	"unicode/utf8"
)

func TestHTTPKeywordsOnLocalServer(t *testing.T) {
	absent := true
	for _, test := range []struct {
		name, body, keyword string
		absent              *bool
		gzip                bool
		code                int
		ok                  bool
		errorText           string
	}{
		{"contains UTF8", "正文欢迎来到博客", "欢迎来到", nil, false, 200, true, ""},
		{"missing", "正文", "欢迎来到", nil, false, 200, false, "正文前 1 MiB 不包含"},
		{"case sensitive", "Welcome", "welcome", nil, false, 200, false, "不包含"},
		{"absent passes", "normal page", "fatal", &absent, false, 200, true, ""},
		{"absent fails", "a fatal page", "fatal", &absent, false, 200, false, "正文包含"},
		{"block overlap", strings.Repeat("x", httpBlockSize-2) + "欢迎来到", "欢迎来到", nil, false, 200, true, ""},
		{"several blocks", strings.Repeat("x", httpBlockSize*3-1) + "needle", "needle", nil, false, 200, true, ""},
		{"at limit", strings.Repeat("x", httpBodyLimit-6) + "needle", "needle", nil, false, 200, true, ""},
		{"past limit", strings.Repeat("x", httpBodyLimit) + "needle", "needle", nil, false, 200, false, "不包含"},
		{"gzip decoded", strings.Repeat("x", httpBlockSize-1) + "欢迎来到", "欢迎来到", nil, true, 200, true, ""},
		{"status first", "fatal", "fatal", &absent, false, 500, false, "HTTP 状态码 500"},
	} {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				var writer io.Writer = w
				if test.gzip {
					w.Header().Set("Content-Encoding", "gzip")
					compressed := gzip.NewWriter(w)
					defer compressed.Close()
					writer = compressed
				}
				w.WriteHeader(test.code)
				_, _ = io.WriteString(writer, test.body)
			}))
			defer server.Close()
			prober := NewProber(time.Now)
			defer prober.Close()
			probe := httpProbe(server.URL)
			probe.Keyword, probe.KeywordAbsent = test.keyword, test.absent
			result := prober.Execute(context.Background(), "fake", 0, probe)
			if result.OK != test.ok || !strings.Contains(result.Error, test.errorText) {
				t.Fatalf("keyword result: %+v", result)
			}
			encoded, err := json.Marshal(result)
			if err != nil || strings.Contains(string(encoded), "正文欢迎来到博客") {
				t.Fatalf("response body escaped: %s %v", encoded, err)
			}
		})
	}
}

type countedHTTPBody struct{ remaining, read, largest int }

func (r *countedHTTPBody) Read(buffer []byte) (int, error) {
	r.largest = max(r.largest, len(buffer))
	n := min(len(buffer), r.remaining)
	for i := range buffer[:n] {
		buffer[i] = 'x'
	}
	r.remaining -= n
	r.read += n
	if n == 0 {
		return 0, io.EOF
	}
	return n, nil
}

func TestHTTPKeywordReadAndAllocationRemainBounded(t *testing.T) {
	body := &countedHTTPBody{remaining: httpBodyLimit * 8}
	found, err := containsHTTPKeyword(body, []byte(strings.Repeat("n", 256)))
	if err != nil || found || body.read != httpBodyLimit || body.largest > httpBlockSize {
		t.Fatalf("unbounded read: %+v found=%t err=%v", body, found, err)
	}
	var allocated []int64
	for _, size := range []int{httpBlockSize, httpBodyLimit * 8} {
		bench := testing.Benchmark(func(b *testing.B) {
			for i := 0; i < b.N; i++ {
				reader := countedHTTPBody{remaining: size}
				_, _ = containsHTTPKeyword(&reader, []byte("needle"))
			}
		})
		allocated = append(allocated, bench.AllocedBytesPerOp())
	}
	if allocated[0] > 64<<10 || allocated[1] > 64<<10 || allocated[1] > allocated[0]+1024 {
		t.Fatalf("buffer allocation grew with response size: %v", allocated)
	}
}

type failingKeywordReader struct {
	err   error
	reads int
}

func (r *failingKeywordReader) Read([]byte) (int, error) { r.reads++; return 0, r.err }

func TestHTTPKeywordStatusPrecedenceAndReadErrors(t *testing.T) {
	failure := errors.New("fake reader failure")
	reader := &failingKeywordReader{err: failure}
	client := doerFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 503, Body: io.NopCloser(reader)}, nil
	})
	prober := newProber(client, nil, time.Now)
	probe := httpProbe("http://fake.invalid/")
	probe.Keyword = strings.Repeat("字", 85)
	result := prober.Execute(context.Background(), "fake", 0, probe)
	if result.OK || !strings.Contains(result.Error, "503") || reader.reads != 0 {
		t.Fatalf("failed status read keyword: %+v reads=%d", result, reader.reads)
	}
	client = func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Body: io.NopCloser(reader)}, nil
	}
	prober = newProber(client, nil, time.Now)
	result = prober.Execute(context.Background(), "fake", 0, probe)
	if result.OK || !errors.Is(result.cause, failure) {
		t.Fatalf("lost body error: %+v", result)
	}
	prober = newProber(doerFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(""))}, nil
	}), nil, time.Now)
	result = prober.Execute(context.Background(), "fake", 0, probe)
	shown := strings.TrimSuffix(strings.Split(result.Error, "“")[1], "”")
	if utf8.RuneCountInString(shown) != 64 {
		t.Fatalf("keyword diagnostic not bounded: %q", shown)
	}
}

func TestHTTPCheckResultValidation(t *testing.T) {
	for _, result := range []ProbeResult{
		{TargetID: "fake", Type: "tcp", TLSNotAfter: 1},
		{TargetID: "fake", Type: "http", TLSSubject: "missing date"},
		{TargetID: "fake", Type: "http", TLSNotAfter: -1},
		{TargetID: "fake", Type: "http", TLSNotAfter: 1, TLSSubject: strings.Repeat("字", 201)},
	} {
		if err := result.Validate(); err == nil {
			t.Fatalf("invalid TLS metadata accepted: %+v", result)
		}
	}
	result := ProbeResult{TargetID: "fake", Type: "http", TLSNotAfter: 1, TLSSubject: strings.Repeat("字", 200)}
	if err := result.Validate(); err != nil {
		t.Fatal(err)
	}
}
