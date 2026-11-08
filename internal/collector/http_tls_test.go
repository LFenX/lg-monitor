package collector

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"io"
	"log"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
	"unicode/utf8"
)

func localHTTPChain(t *testing.T, now time.Time, leafDays, intermediateDays, rootDays int, subject string, correctName bool) (tls.Certificate, *x509.CertPool) {
	t.Helper()
	issue := func(template, parent *x509.Certificate, parentKey *ecdsa.PrivateKey) (*x509.Certificate, *ecdsa.PrivateKey) {
		t.Helper()
		key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
		if err != nil {
			t.Fatal(err)
		}
		if parent == nil {
			parent, parentKey = template, key
		}
		der, err := x509.CreateCertificate(rand.Reader, template, parent, &key.PublicKey, parentKey)
		if err != nil {
			t.Fatal(err)
		}
		certificate, err := x509.ParseCertificate(der)
		if err != nil {
			t.Fatal(err)
		}
		return certificate, key
	}
	root, rootKey := issue(&x509.Certificate{
		SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "fake root"}, IsCA: true,
		BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign,
		NotBefore: now.Add(-365 * 24 * time.Hour), NotAfter: now.Add(time.Duration(rootDays) * 24 * time.Hour),
	}, nil, nil)
	intermediate, intermediateKey := issue(&x509.Certificate{
		SerialNumber: big.NewInt(2), Subject: pkix.Name{CommonName: "fake intermediate"}, IsCA: true,
		BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign,
		NotBefore: now.Add(-365 * 24 * time.Hour), NotAfter: now.Add(time.Duration(intermediateDays) * 24 * time.Hour),
	}, root, rootKey)
	address := net.ParseIP("127.0.0.1")
	if !correctName {
		address = net.ParseIP("192.0.2.1")
	}
	leaf, key := issue(&x509.Certificate{
		SerialNumber: big.NewInt(3), Subject: pkix.Name{CommonName: subject},
		DNSNames: []string{"fixture.invalid"}, IPAddresses: []net.IP{address},
		KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		NotBefore: now.Add(-365 * 24 * time.Hour), NotAfter: now.Add(time.Duration(leafDays) * 24 * time.Hour),
	}, intermediate, intermediateKey)
	roots := x509.NewCertPool()
	roots.AddCert(root)
	return tls.Certificate{Certificate: [][]byte{leaf.Raw, intermediate.Raw, root.Raw}, PrivateKey: key, Leaf: leaf}, roots
}

func trustedHTTPProber(now time.Time, roots *x509.CertPool) *Prober {
	prober := NewProber(func() time.Time { return now })
	prober.http.(*http.Client).Transport.(*http.Transport).TLSClientConfig.RootCAs = roots
	return prober
}

func TestHTTPSRecordsEarliestCertificateIncludingVerificationFailures(t *testing.T) {
	now := time.Date(2030, 1, 2, 3, 4, 5, 0, time.UTC)
	for _, test := range []struct {
		name                               string
		leaf, intermediate, root, expected int
		subject                            string
		correctName, trusted               bool
		errorText                          string
	}{
		{"normal", 90, 365, 500, 90, "fake leaf", true, true, ""},
		{"soon", 3, 365, 500, 3, "fake leaf", true, true, ""},
		{"expired", -1, 365, 500, -1, "fake leaf", true, true, "已过期"},
		{"intermediate first", 90, 10, 500, 10, "fake intermediate", true, true, ""},
		{"expired intermediate", 90, -1, 500, -1, "fake intermediate", true, true, "已过期"},
		{"skip earlier root", 90, 30, 2, 30, "fake intermediate", true, true, ""},
		{"DNS fallback", 90, 365, 500, 90, "fixture.invalid", true, true, ""},
		{"hostname", 90, 365, 500, 90, "fake leaf", false, true, "域名不符"},
		{"untrusted", 90, 365, 500, 90, "fake leaf", true, false, "颁发者不受信任"},
	} {
		t.Run(test.name, func(t *testing.T) {
			cn := "fake leaf"
			if test.name == "DNS fallback" {
				cn = ""
			}
			certificate, roots := localHTTPChain(t, now, test.leaf, test.intermediate, test.root, cn, test.correctName)
			server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, "ok") }))
			server.Config.ErrorLog = log.New(io.Discard, "", 0)
			server.TLS = &tls.Config{Certificates: []tls.Certificate{certificate}}
			server.StartTLS()
			defer server.Close()
			if !test.trusted {
				roots = x509.NewCertPool()
			}
			prober := trustedHTTPProber(now, roots)
			defer prober.Close()
			result := prober.Execute(context.Background(), "fake", 0, httpProbe(server.URL+"?token=fake-do-not-persist"))
			if result.OK != (test.errorText == "") || !strings.Contains(result.Error, test.errorText) ||
				result.TLSNotAfter != now.Add(time.Duration(test.expected)*24*time.Hour).UnixMilli() || result.TLSSubject != test.subject {
				t.Fatalf("certificate result: %+v", result)
			}
			encoded, _ := json.Marshal(result)
			if strings.Contains(string(encoded), "fake-do-not-persist") || strings.Contains(result.Error, server.URL) {
				t.Fatalf("URL leaked: %s", encoded)
			}
		})
	}
}

func TestHTTPSRenewalUsesANewConnectionImmediately(t *testing.T) {
	now := time.Date(2030, 1, 2, 3, 4, 5, 0, time.UTC)
	first, roots := localHTTPChain(t, now, 3, 365, 500, strings.Repeat("字", 201), true)
	second, _ := localHTTPChain(t, now, 90, 365, 500, "renewed leaf", true)
	// Both authorities are local test roots; no global trust store is changed.
	secondRoot, err := x509.ParseCertificate(second.Certificate[2])
	if err != nil {
		t.Fatal(err)
	}
	roots.AddCert(secondRoot)
	var selected atomic.Pointer[tls.Certificate]
	selected.Store(&first)
	var connections atomic.Int32
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, "ok") }))
	server.EnableHTTP2 = true
	server.Config.ConnState = func(_ net.Conn, state http.ConnState) {
		if state == http.StateNew {
			connections.Add(1)
		}
	}
	server.TLS = &tls.Config{GetConfigForClient: func(*tls.ClientHelloInfo) (*tls.Config, error) {
		return &tls.Config{Certificates: []tls.Certificate{*selected.Load()}, NextProtos: []string{"h2", "http/1.1"}}, nil
	}}
	server.StartTLS()
	defer server.Close()
	prober := trustedHTTPProber(now, roots)
	defer prober.Close()
	a := prober.Execute(context.Background(), "fake", 0, httpProbe(server.URL))
	selected.Store(&second)
	b := prober.Execute(context.Background(), "fake", 0, httpProbe(server.URL))
	if !a.OK || !b.OK || a.TLSNotAfter != first.Leaf.NotAfter.UnixMilli() || b.TLSNotAfter != second.Leaf.NotAfter.UnixMilli() ||
		b.TLSSubject != "renewed leaf" || connections.Load() != 2 || utf8.RuneCountInString(a.TLSSubject) != 200 {
		t.Fatalf("TLS connection or subject was reused: a=%+v b=%+v connections=%d", a, b, connections.Load())
	}
}

func TestHTTPSRetainsVerifiedCertificateWhenHTTPHeadersFail(t *testing.T) {
	now := time.Date(2030, 1, 2, 3, 4, 5, 0, time.UTC)
	certificate, roots := localHTTPChain(t, now, 3, 365, 500, "fake leaf", true)
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		connection, _, err := w.(http.Hijacker).Hijack()
		if err == nil {
			_ = connection.Close()
		}
	}))
	server.TLS = &tls.Config{Certificates: []tls.Certificate{certificate}}
	server.StartTLS()
	defer server.Close()
	prober := trustedHTTPProber(now, roots)
	defer prober.Close()
	result := prober.Execute(context.Background(), "fake", 0, httpProbe(server.URL))
	if result.OK || result.TLSNotAfter != certificate.Leaf.NotAfter.UnixMilli() {
		t.Fatalf("verified handshake was lost after HTTP failure: %+v", result)
	}
}
