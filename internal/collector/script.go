package collector

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"regexp"
	"strings"
	"sync"
	"unicode/utf8"

	"github.com/LFenX/lg-monitor/internal/config"
	"github.com/LFenX/lg-monitor/internal/secrets"
)

const MaxScriptOutput = 64 * 1024
const MaxScriptDiagnostic = 32 * 1024

type ScriptOutput struct {
	Stdout   []byte
	Tail     string
	ExitCode uint32
}

type ScriptRunner interface {
	Run(context.Context, ScriptCommand) (ScriptOutput, error)
}

type ScriptCommand struct {
	Name string
	Args []string
}

type nativeScriptRunner struct {
	home, hostname string
	policy         secrets.PathPolicy
}

type scriptError struct {
	message string
	cause   error
}

func (e *scriptError) Error() string { return e.message }
func (e *scriptError) Unwrap() error { return e.cause }

type scriptDocument struct {
	OK      *bool                      `json:"ok"`
	Message string                     `json:"message"`
	Metrics map[string]json.RawMessage `json:"metrics"`
}

var scriptMetricName = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_.-]{0,127}$`)

func (p *Prober) script(ctx context.Context, result ProbeResult, probe config.Probe) ProbeResult {
	if err := config.ValidateScript(probe.Script, probe.Args); err != nil {
		result.Error, result.cause = err.Error(), err
		return result
	}
	output, err := p.scripts.Run(ctx, ScriptCommand{Name: probe.Script, Args: append([]string(nil), probe.Args...)})
	fail := func(message string, cause error) ProbeResult {
		result.OK, result.Error, result.cause = false, message, cause
		if tail := lastLines(output.Tail, 20); tail != "" {
			result.Error += "\n最近输出：\n" + tail
		}
		return result
	}
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) || errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return fail("脚本执行超时，请检查脚本或增大 timeout", err)
		}
		if errors.Is(err, context.Canceled) || errors.Is(ctx.Err(), context.Canceled) {
			return fail("脚本执行已取消", err)
		}
		var diagnostic *scriptError
		if errors.As(err, &diagnostic) {
			return fail(diagnostic.message, err)
		}
		return fail("脚本启动或输出读取失败，请检查文件和权限", err)
	}
	if output.ExitCode != 0 {
		return fail(fmt.Sprintf("脚本退出码为 %d", output.ExitCode), nil)
	}
	line := bytes.TrimSpace(output.Stdout)
	if index := bytes.LastIndexByte(line, '\n'); index >= 0 {
		line = bytes.TrimSpace(line[index+1:])
	}
	if !utf8.Valid(line) {
		return fail("脚本末行必须为 UTF-8 JSON", nil)
	}
	var document scriptDocument
	decoder := json.NewDecoder(bytes.NewReader(line))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&document); err != nil {
		return fail("脚本末行不是约定格式的 JSON", err)
	}
	if err := decoder.Decode(new(any)); err != io.EOF || document.OK == nil {
		return fail("脚本 JSON 必须包含布尔 ok 且只有一个对象", err)
	}
	if len(document.Message) > 4096 || len(document.Metrics) > 256 {
		return fail("脚本 message 超过 4096 字节或 metrics 超过 256 项", nil)
	}
	var metrics map[string]float64
	if document.Metrics != nil {
		metrics = make(map[string]float64, len(document.Metrics))
	}
	for name, raw := range document.Metrics {
		decoder := json.NewDecoder(bytes.NewReader(raw))
		decoder.UseNumber()
		var decoded any
		if err := decoder.Decode(&decoded); err != nil {
			return fail("脚本指标必须是有限数字", err)
		}
		number, ok := decoded.(json.Number)
		if !ok {
			return fail("脚本指标必须是数字，不能使用字符串或 null", nil)
		}
		value, err := number.Float64()
		if err != nil || !scriptMetricName.MatchString(name) || math.IsNaN(value) || math.IsInf(value, 0) {
			return fail("脚本指标名称或数值无效", err)
		}
		metrics[name] = value
	}
	result.OK, result.Message, result.Metrics = *document.OK, document.Message, metrics
	if !result.OK {
		return fail("脚本检查失败："+document.Message, nil)
	}
	return result
}

func lastLines(value string, count int) string {
	value = strings.TrimRight(strings.ToValidUTF8(value, "�"), "\r\n")
	lines := strings.Split(value, "\n")
	if len(lines) > count {
		lines = lines[len(lines)-count:]
	}
	value = strings.Join(lines, "\n")
	if len(value) > MaxScriptDiagnostic {
		const prefix = "[输出已截断]\n"
		start := len(value) - (MaxScriptDiagnostic - len(prefix))
		for start < len(value) && !utf8.RuneStart(value[start]) {
			start++
		}
		value = prefix + value[start:]
	}
	return value
}

type scriptCapture struct {
	mu           sync.Mutex
	stdout, tail []byte
}

func appendTail(previous, data []byte, limit int) []byte {
	if len(data) >= limit {
		return append(previous[:0], data[len(data)-limit:]...)
	}
	if overflow := len(previous) + len(data) - limit; overflow > 0 {
		copy(previous, previous[overflow:])
		previous = previous[:len(previous)-overflow]
	}
	return append(previous, data...)
}

func (c *scriptCapture) write(data []byte, stdout bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if stdout {
		c.stdout = appendTail(c.stdout, data, MaxScriptOutput)
	}
	c.tail = appendTail(c.tail, data, MaxScriptDiagnostic)
}

func (c *scriptCapture) output(exitCode uint32) ScriptOutput {
	c.mu.Lock()
	defer c.mu.Unlock()
	return ScriptOutput{Stdout: append([]byte(nil), c.stdout...), Tail: string(c.tail), ExitCode: exitCode}
}
