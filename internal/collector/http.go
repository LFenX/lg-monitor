package collector

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptrace"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/LFenX/lg-monitor/internal/config"
)

const httpBodyLimit = 1 << 20
const httpBlockSize = 32 << 10

func (p *Prober) executeHTTP(ctx context.Context, result ProbeResult, probe config.Probe) ProbeResult {
	if len(probe.Keyword) > 256 || !utf8.ValidString(probe.Keyword) || (probe.KeywordAbsent != nil && probe.Keyword == "") {
		result.Error = "keyword 必须为 1～256 个 UTF-8 字节，keywordAbsent 必须同时配置 keyword"
		return result
	}
	var mu sync.Mutex
	var certificate *x509.Certificate
	trace := &httptrace.ClientTrace{TLSHandshakeDone: func(state tls.ConnectionState, err error) {
		mu.Lock()
		certificate = observedCertificate(&state, err)
		mu.Unlock()
	}}
	request, err := http.NewRequestWithContext(httptrace.WithClientTrace(ctx, trace), http.MethodGet, probe.URL, nil)
	if err != nil {
		probeFailure(&result, "HTTP 探测地址无效", err)
		return result
	}
	response, err := p.http.Do(request)
	if response != nil && response.Body != nil {
		defer response.Body.Close()
	}
	mu.Lock()
	observed := certificate
	mu.Unlock()
	if response != nil && response.TLS != nil {
		observed = observedCertificate(response.TLS, err)
	}
	if observed == nil {
		observed = observedCertificate(nil, err)
	}
	if observed != nil {
		result.TLSNotAfter = observed.NotAfter.UnixMilli()
		subject := observed.Subject.CommonName
		if subject == "" && len(observed.DNSNames) > 0 {
			subject = observed.DNSNames[0]
		}
		result.TLSSubject = clipHTTPText(subject, 200)
	}
	if err != nil {
		probeFailure(&result, "HTTP 探测请求失败", err)
		if diagnostic := certificateDiagnostic(err, p.now()); diagnostic != "" {
			result.Error = diagnostic
		}
		return result
	}
	if response == nil {
		result.Error = "HTTP 探测未收到响应"
		return result
	}
	result.StatusCode = response.StatusCode
	if !statusMatches(probe.ExpectStatus, response.StatusCode) {
		result.Error = fmt.Sprintf("HTTP 状态码 %d 不符合期望 %s", response.StatusCode, expectedStatus(probe.ExpectStatus))
		return result
	}
	if probe.Keyword != "" {
		body := response.Body
		if body == nil {
			body = io.NopCloser(strings.NewReader(""))
		}
		found, err := containsHTTPKeyword(body, []byte(probe.Keyword))
		if err != nil {
			probeFailure(&result, "读取 HTTP 探测响应失败", err)
			return result
		}
		absent := probe.KeywordAbsent != nil && *probe.KeywordAbsent
		if found == absent {
			if absent {
				result.Error = fmt.Sprintf("正文包含关键字“%s”", clipHTTPText(probe.Keyword, 64))
			} else {
				result.Error = fmt.Sprintf("正文前 1 MiB 不包含关键字“%s”", clipHTTPText(probe.Keyword, 64))
			}
			return result
		}
	} else if response.Body != nil {
		if _, err := io.Copy(io.Discard, io.LimitReader(response.Body, httpBlockSize)); err != nil {
			probeFailure(&result, "读取 HTTP 探测响应失败", err)
			return result
		}
	}
	result.OK = true
	return result
}

func containsHTTPKeyword(reader io.Reader, keyword []byte) (bool, error) {
	if len(keyword) == 0 || len(keyword) > 256 {
		return false, fmt.Errorf("keyword 必须为 1～256 个字节")
	}
	// Keep only the overlap; retaining the entire body would multiply collector RSS by probe count.
	buffer := make([]byte, httpBlockSize+len(keyword)-1)
	bounded := io.LimitReader(reader, httpBodyLimit)
	carry := 0
	for {
		n, err := io.ReadFull(bounded, buffer[carry:carry+httpBlockSize])
		if bytes.Contains(buffer[:carry+n], keyword) {
			return true, nil
		}
		if err != nil {
			if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
				return false, nil
			}
			return false, fmt.Errorf("读取关键字检查块失败：%w", err)
		}
		total := carry + n
		carry = min(len(keyword)-1, total)
		copy(buffer[:carry], buffer[total-carry:total])
	}
}

func observedCertificate(state *tls.ConnectionState, failure error) *x509.Certificate {
	var chain []*x509.Certificate
	if state != nil && len(state.VerifiedChains) > 0 {
		chain = state.VerifiedChains[0]
	} else {
		var verification *tls.CertificateVerificationError
		if errors.As(failure, &verification) {
			chain = verification.UnverifiedCertificates
		}
	}
	var earliest *x509.Certificate
	for index, certificate := range chain {
		if certificate == nil {
			continue
		}
		if index > 0 && bytes.Equal(certificate.RawIssuer, certificate.RawSubject) && certificate.CheckSignatureFrom(certificate) == nil {
			continue
		}
		if earliest == nil || certificate.NotAfter.Before(earliest.NotAfter) {
			earliest = certificate
		}
	}
	return earliest
}

func certificateDiagnostic(err error, now time.Time) string {
	var invalid x509.CertificateInvalidError
	var hostname x509.HostnameError
	var issuer x509.UnknownAuthorityError
	switch {
	case errors.As(err, &invalid) && invalid.Reason == x509.Expired:
		if invalid.Cert != nil && now.Before(invalid.Cert.NotBefore) {
			return "HTTPS 证书尚未生效"
		}
		if invalid.Cert != nil {
			return "HTTPS 证书已过期（" + invalid.Cert.NotAfter.In(now.Location()).Format("2006-01-02") + "）"
		}
		return "HTTPS 证书已过期"
	case errors.As(err, &hostname):
		return "HTTPS 证书域名不符"
	case errors.As(err, &issuer):
		return "HTTPS 证书颁发者不受信任"
	default:
		return ""
	}
}

func clipHTTPText(text string, limit int) string {
	runes := []rune(text)
	if len(runes) <= limit {
		return text
	}
	return string(runes[:limit-1]) + "…"
}
