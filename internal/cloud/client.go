// Copyright (c) 2026 LFen (https://lfen.ink) — lg-monitor

package cloud

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// ClientResult captures response status, headers, body, and exact wire traffic bytes.
type ClientResult struct {
	Status        int
	Body          []byte
	ETag          string
	BytesSent     int64
	BytesReceived int64
}

// Client abstracts HTTP calls to the cloud sync worker for testability.
type Client interface {
	Do(ctx context.Context, method, baseURL, path, token, ifNoneMatch string, body any, compress bool) (ClientResult, error)
}

// HTTPClient is the real network implementation with automatic gzip request compression.
type HTTPClient struct {
	HTTP *http.Client
}

func (c HTTPClient) Do(ctx context.Context, method, baseURL, path, token, ifNoneMatch string, body any, compress bool) (ClientResult, error) {
	var result ClientResult
	var reqBody io.Reader
	gzipped := false

	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			return result, fmt.Errorf("编码同步请求失败：%w", err)
		}
		if compress && len(raw) >= 256 {
			var buf bytes.Buffer
			gz, _ := gzip.NewWriterLevel(&buf, gzip.DefaultCompression)
			if _, err := gz.Write(raw); err == nil && gz.Close() == nil && buf.Len() < len(raw) {
				reqBody = bytes.NewReader(buf.Bytes())
				result.BytesSent = int64(buf.Len())
				gzipped = true
			}
		}
		if reqBody == nil {
			reqBody = bytes.NewReader(raw)
			result.BytesSent = int64(len(raw))
		}
	}

	url := strings.TrimRight(strings.TrimSpace(baseURL), "/") + path
	req, err := http.NewRequestWithContext(ctx, method, url, reqBody)
	if err != nil {
		return result, fmt.Errorf("构造云同步请求失败：%w", err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if gzipped {
		req.Header.Set("Content-Encoding", "gzip")
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Accept-Encoding", "gzip")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	if ifNoneMatch != "" {
		req.Header.Set("If-None-Match", ifNoneMatch)
	}

	httpClient := c.HTTP
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 15 * time.Second}
	}
	resp, err := httpClient.Do(req)
	if err != nil {
		return result, fmt.Errorf("连不上云同步服务：%w", err)
	}
	defer resp.Body.Close()

	result.Status = resp.StatusCode
	result.ETag = resp.Header.Get("ETag")

	if resp.StatusCode == http.StatusNotModified {
		return result, nil
	}

	limited := io.LimitReader(resp.Body, 2<<20)
	var reader io.Reader = limited
	// Count wire bytes received (including compressed size when gzip is served).
	rawWire, err := io.ReadAll(reader)
	if err != nil {
		return result, fmt.Errorf("读取云同步响应失败：%w", err)
	}
	result.BytesReceived = int64(len(rawWire))

	if strings.EqualFold(resp.Header.Get("Content-Encoding"), "gzip") && len(rawWire) > 0 {
		gz, err := gzip.NewReader(bytes.NewReader(rawWire))
		if err != nil {
			return result, fmt.Errorf("解压云同步响应失败：%w", err)
		}
		defer gz.Close()
		unpacked, err := io.ReadAll(io.LimitReader(gz, 4<<20))
		if err != nil {
			return result, fmt.Errorf("解压云同步响应失败：%w", err)
		}
		result.Body = unpacked
	} else {
		result.Body = rawWire
	}
	return result, nil
}

// RemoteError represents a structured error returned by the cloud worker.
type RemoteError struct {
	Status  int
	Code    string
	Message string
}

func (e *RemoteError) Error() string {
	if e.Message != "" {
		return e.Message
	}
	return fmt.Sprintf("云同步服务返回 HTTP %d", e.Status)
}

func parseRemoteError(status int, body []byte) error {
	var envelope struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(body, &envelope); err == nil && envelope.Error.Message != "" {
		return &RemoteError{Status: status, Code: envelope.Error.Code, Message: envelope.Error.Message}
	}
	return &RemoteError{Status: status, Code: "cloud_error", Message: fmt.Sprintf("云同步服务返回错误（HTTP %d）", status)}
}

func IsUnauthorized(err error) bool {
	var re *RemoteError
	if errors.As(err, &re) {
		// A bare 401 may come from a proxy or a captive portal in between; only the service's own answer counts.
		return (re.Status == 401 && re.Code != "cloud_error") || re.Code == "device_unbound"
	}
	return false
}
