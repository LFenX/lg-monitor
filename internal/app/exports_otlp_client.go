package app

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"io"
	"math"
	"mime"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/LFenX/lg-monitor/internal/config"
	"github.com/LFenX/lg-monitor/internal/notify"
	collectorpb "go.opentelemetry.io/proto/otlp/collector/metrics/v1"
	"google.golang.org/protobuf/proto"
)

const maxOTLPResponseBytes = 64 * 1024

type OTLPHTTPClient interface {
	Do(*http.Request) (*http.Response, error)
	CloseIdleConnections()
}

type otlpExportError struct {
	class  string
	status int
	cause  error
}

func (e *otlpExportError) Error() string { return "OTLP 指标导出失败（" + e.class + "）" }
func (e *otlpExportError) Unwrap() error { return e.cause }

func newOTLPHTTPClient(ctx context.Context, settings config.OTLPSettings, secrets SecretReader, now func() time.Time) (OTLPHTTPClient, string, error) {
	endpoint, err := settings.EndpointURL()
	if err != nil {
		return nil, "", &otlpExportError{class: "config", cause: err}
	}
	get := func(ref string) ([]byte, error) {
		if ref == "" {
			return nil, nil
		}
		if secrets == nil {
			return nil, fmt.Errorf("OTLP 凭据需要本机加密密钥库")
		}
		value, err := secrets.Get(ctx, ref)
		if err != nil {
			return nil, fmt.Errorf("读取 OTLP 加密凭据失败：%w", err)
		}
		if len(value) == 0 || len(value) > 1024*1024 {
			return nil, fmt.Errorf("OTLP 凭据为空或超过大小界限")
		}
		return value, nil
	}
	authorization, err := get(settings.AuthorizationRef)
	if err != nil {
		return nil, "", &otlpExportError{class: "credentials", cause: err}
	}
	defer clear(authorization)
	for _, c := range authorization {
		if c < 32 || c > 126 {
			return nil, "", &otlpExportError{class: "credentials", cause: fmt.Errorf("Authorization 凭据必须为单行 ASCII 请求头")}
		}
	}
	if len(authorization) > 8192 {
		return nil, "", &otlpExportError{class: "credentials", cause: fmt.Errorf("Authorization 凭据超过请求头界限")}
	}
	tlsConfig := &tls.Config{MinVersion: tls.VersionTLS12, Time: now}
	if settings.TLS.CARef != "" {
		pem, err := get(settings.TLS.CARef)
		if err != nil {
			return nil, "", &otlpExportError{class: "credentials", cause: err}
		}
		roots, err := x509.SystemCertPool()
		if err != nil {
			clear(pem)
			return nil, "", &otlpExportError{class: "tls", cause: err}
		}
		if !roots.AppendCertsFromPEM(pem) {
			clear(pem)
			return nil, "", &otlpExportError{class: "tls", cause: fmt.Errorf("OTLP CA 引用不是有效 PEM 证书")}
		}
		clear(pem)
		tlsConfig.RootCAs = roots
	}
	if settings.TLS.CertRef != "" || settings.TLS.KeyRef != "" {
		cert, err := get(settings.TLS.CertRef)
		if err != nil {
			return nil, "", &otlpExportError{class: "credentials", cause: err}
		}
		defer clear(cert)
		key, err := get(settings.TLS.KeyRef)
		if err != nil {
			return nil, "", &otlpExportError{class: "credentials", cause: err}
		}
		defer clear(key)
		pair, err := tls.X509KeyPair(cert, key)
		if err != nil {
			return nil, "", &otlpExportError{class: "tls", cause: err}
		}
		tlsConfig.Certificates = []tls.Certificate{pair}
	}
	dialer := &net.Dialer{Timeout: settings.EffectiveTimeout(), KeepAlive: 30 * time.Second}
	dial := dialer.DialContext
	if endpoint.Scheme == "http" {
		// Resolve localhost once and check every candidate before sending any credentials.
		dial = func(ctx context.Context, network, address string) (net.Conn, error) {
			host, port, err := net.SplitHostPort(address)
			if err != nil {
				return nil, err
			}
			addresses, err := net.DefaultResolver.LookupNetIP(ctx, "ip", host)
			if err != nil {
				return nil, err
			}
			if len(addresses) == 0 {
				return nil, fmt.Errorf("OTLP 回环地址没有解析结果")
			}
			for _, address := range addresses {
				if !address.Unmap().IsLoopback() {
					return nil, fmt.Errorf("OTLP HTTP 解析结果不是回环地址")
				}
			}
			var failure error
			for _, address := range addresses {
				connection, err := dialer.DialContext(ctx, network, net.JoinHostPort(address.String(), port))
				if err == nil {
					return connection, nil
				}
				failure = err
			}
			return nil, failure
		}
	}
	transport := &http.Transport{Proxy: http.ProxyFromEnvironment, DialContext: dial, TLSClientConfig: tlsConfig, ForceAttemptHTTP2: true, MaxIdleConns: 2, MaxIdleConnsPerHost: 1, MaxConnsPerHost: 1, IdleConnTimeout: time.Minute, TLSHandshakeTimeout: settings.EffectiveTimeout(), ResponseHeaderTimeout: settings.EffectiveTimeout(), MaxResponseHeaderBytes: 16 * 1024}
	if endpoint.Scheme == "http" {
		transport.Proxy = nil
	}
	client := &http.Client{Transport: transport, Timeout: settings.EffectiveTimeout(), CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	return client, string(authorization), nil
}

type nativeOTLPWaiter struct{}

func (nativeOTLPWaiter) Wait(ctx context.Context, d time.Duration) error {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func otlpRetryAfter(value string, now time.Time) time.Duration {
	if value == "" {
		return 0
	}
	seconds, err := strconv.ParseInt(value, 10, 64)
	if err == nil && seconds >= 0 {
		if seconds > math.MaxInt64/int64(time.Second) {
			return time.Duration(math.MaxInt64)
		}
		return time.Duration(seconds) * time.Second
	}
	if strings.Trim(value, "0123456789") == "" {
		return time.Duration(math.MaxInt64)
	}
	when, err := http.ParseTime(value)
	if err != nil {
		return 0
	}
	return max(0, when.Sub(now))
}

func sendOTLP(ctx context.Context, client OTLPHTTPClient, endpoint, authorization string, payload []byte, now func() time.Time, waiter notify.Waiter, jitter func(time.Duration) time.Duration) (int64, bool, error) {
	for attempt := 0; attempt < 3; attempt++ {
		request, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(payload))
		if err != nil {
			return 0, false, &otlpExportError{class: "config", cause: err}
		}
		request.Header.Set("Content-Type", "application/x-protobuf")
		request.Header.Set("Accept", "application/x-protobuf")
		if authorization != "" {
			request.Header.Set("Authorization", authorization)
		}
		response, err := client.Do(request)
		retry, delay := false, time.Duration(0)
		var failure error
		if err != nil {
			if response != nil && response.Body != nil {
				response.Body.Close()
			}
			var certificate *tls.CertificateVerificationError
			class := "transport"
			if errors.As(err, &certificate) {
				class = "tls"
			}
			if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
				return 0, false, &otlpExportError{class: "timeout", cause: err}
			}
			failure = &otlpExportError{class: class, cause: err}
			retry = class == "transport"
		} else {
			if response == nil || response.Body == nil {
				return 0, false, &otlpExportError{class: "protocol", cause: fmt.Errorf("OTLP 响应缺少消息体")}
			}
			body, readErr := io.ReadAll(io.LimitReader(response.Body, maxOTLPResponseBytes+1))
			closeErr := response.Body.Close()
			if len(body) > maxOTLPResponseBytes {
				return 0, false, &otlpExportError{class: "response_size", status: response.StatusCode}
			}
			if readErr != nil || closeErr != nil {
				if ctx.Err() != nil {
					return 0, false, &otlpExportError{class: "timeout", status: response.StatusCode, cause: ctx.Err()}
				}
				return 0, false, &otlpExportError{class: "protocol", status: response.StatusCode, cause: errors.Join(readErr, closeErr)}
			}
			if response.StatusCode == http.StatusOK {
				media, _, err := mime.ParseMediaType(response.Header.Get("Content-Type"))
				if err != nil || media != "application/x-protobuf" {
					return 0, false, &otlpExportError{class: "protocol", status: response.StatusCode, cause: err}
				}
				var decoded collectorpb.ExportMetricsServiceResponse
				if err := proto.Unmarshal(body, &decoded); err != nil {
					return 0, false, &otlpExportError{class: "protocol", status: response.StatusCode, cause: err}
				}
				if partial := decoded.PartialSuccess; partial != nil {
					if partial.RejectedDataPoints < 0 {
						return 0, false, &otlpExportError{class: "protocol", status: response.StatusCode}
					}
					return partial.RejectedDataPoints, true, nil
				}
				return 0, false, nil
			}
			status := response.StatusCode
			retry = status == 429 || status == 502 || status == 503 || status == 504
			delay = otlpRetryAfter(response.Header.Get("Retry-After"), now())
			class := "http"
			if status == 429 {
				class = "throttled"
			}
			failure = &otlpExportError{class: class, status: status}
		}
		if !retry || attempt == 2 {
			return 0, false, failure
		}
		delay = max(delay, jitter(200*time.Millisecond*time.Duration(1<<attempt)))
		if err := waiter.Wait(ctx, delay); err != nil {
			return 0, false, &otlpExportError{class: "timeout", cause: err}
		}
	}
	return 0, false, &otlpExportError{class: "protocol"}
}
