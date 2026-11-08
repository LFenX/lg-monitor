package collector

import (
	"bytes"
	"context"
	"encoding/xml"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"path"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

const EventLogMessageBytes = 2048
const EventLogEmptyBookmark = "lgmon-empty-eventlog-v1"

var ErrEventLogBookmarkStale = errors.New("事件日志书签已失效")
var ErrEventLogQueryComplex = errors.New("事件日志筛选条件过于复杂")

type EventLogFilter struct {
	Provider string
	IDs      []uint16
}

type EventLogQuery struct {
	Channel  string
	Filters  []EventLogFilter
	Bookmark string
	Oldest   bool
}

type EventLogRecord struct {
	Channel  string           `json:"channel"`
	Provider string           `json:"provider"`
	EventID  uint32           `json:"eventId"`
	Level    uint8            `json:"level"`
	RecordID uint64           `json:"recordId,string"`
	TS       int64            `json:"ts"`
	Subject  string           `json:"subject"`
	Message  string           `json:"message"`
	Presets  []string         `json:"presets"`
	Targets  []EventLogTarget `json:"targets"`
}

type EventLogTarget struct {
	ID  string `json:"id"`
	Via string `json:"via"`
}

type EventLogNotice struct {
	Kind    string `json:"kind"`
	Channel string `json:"channel"`
	Message string `json:"message"`
	Count   uint64 `json:"count"`
}

func EventLogPresets(e EventLogRecord) []string {
	switch {
	case e.Channel == "Application" && ((e.Provider == "Application Error" && e.EventID == 1000) || (e.Provider == "Application Hang" && e.EventID == 1002)):
		return []string{"app_crash"}
	case e.Channel == "System" && e.Provider == "Service Control Manager" && (e.EventID == 7031 || e.EventID == 7034):
		return []string{"service_crash"}
	case e.Channel == "System" && ((e.Provider == "EventLog" && e.EventID == 6008) || (e.Provider == "Microsoft-Windows-Kernel-Power" && e.EventID == 41)):
		return []string{"unexpected_shutdown"}
	default:
		return nil
	}
}

func (n EventLogNotice) Validate() error {
	if !EventLogChannel(n.Channel) || n.Message == "" || len(n.Message) > EventLogMessageBytes || !utf8.ValidString(n.Message) || n.Count > math.MaxInt64 {
		return fmt.Errorf("事件日志缺口的日志、原因或数量无效")
	}
	if n.Kind != "eventlog_gap" && n.Kind != "eventlog_dropped" || n.Kind == "eventlog_dropped" && n.Count == 0 {
		return fmt.Errorf("事件日志缺口类型无效，丢弃事件数量须为正数")
	}
	return nil
}

type EventLogItem struct {
	Event    EventLogRecord
	Bookmark string
}

type EventLogReader interface {
	Next(context.Context) (EventLogItem, error)
	Close() error
}

type EventLogSource interface {
	Anchor(context.Context, string) (string, error)
	Subscribe(context.Context, EventLogQuery) (EventLogReader, error)
}

type eventLogBackend interface {
	Anchor(context.Context, string) (string, error)
	Open(context.Context, string, string, string, bool) (EventLogReader, error)
}

type eventLogSource struct {
	backend eventLogBackend
	logger  *slog.Logger
}

func NewEventLogSource(logger *slog.Logger) EventLogSource {
	if logger == nil {
		logger = slog.Default()
	}
	return eventLogSource{backend: nativeEventLogBackend{}, logger: logger}
}

func EventLogChannel(channel string) bool { return channel == "Application" || channel == "System" }

func EventLogBookmarkRecordID(bookmark, channel string) (uint64, error) {
	if !EventLogChannel(channel) {
		return 0, fmt.Errorf("事件日志只允许 Application 和 System")
	}
	if bookmark == EventLogEmptyBookmark {
		return 0, nil
	}
	var value struct {
		Bookmarks []struct {
			Channel string `xml:"Channel,attr"`
			ID      uint64 `xml:"RecordId,attr"`
		} `xml:"Bookmark"`
	}
	if err := xml.Unmarshal([]byte(bookmark), &value); err != nil {
		return 0, fmt.Errorf("解析事件日志订阅生效位置失败：%w", err)
	}
	if len(value.Bookmarks) != 1 || value.Bookmarks[0].Channel != channel || value.Bookmarks[0].ID == 0 {
		return 0, fmt.Errorf("事件日志书签缺少匹配的记录位置")
	}
	return value.Bookmarks[0].ID, nil
}

func (s eventLogSource) Anchor(ctx context.Context, channel string) (string, error) {
	if !EventLogChannel(channel) {
		return "", fmt.Errorf("事件日志只允许 Application 和 System")
	}
	if err := ctx.Err(); err != nil {
		return "", fmt.Errorf("获取事件日志起点已取消：%w", err)
	}
	return s.backend.Anchor(ctx, channel)
}

func (s eventLogSource) Subscribe(ctx context.Context, request EventLogQuery) (EventLogReader, error) {
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("订阅事件日志已取消：%w", err)
	}
	if !EventLogChannel(request.Channel) {
		return nil, fmt.Errorf("事件日志只允许 Application 和 System")
	}
	if request.Oldest && request.Bookmark != "" {
		return nil, fmt.Errorf("从最早事件读取时不能同时指定书签")
	}
	if request.Bookmark == EventLogEmptyBookmark {
		request.Bookmark, request.Oldest = "", true
	}
	for _, filter := range request.Filters {
		if strings.TrimSpace(filter.Provider) == "" || strings.ContainsRune(filter.Provider, 0) || !utf8.ValidString(filter.Provider) {
			return nil, fmt.Errorf("事件日志来源不能为空，且须为不含 NUL 的 UTF-8 文本")
		}
	}
	query := eventLogQuery(request.Channel, request.Filters, false)
	reader, err := s.backend.Open(ctx, request.Channel, query, request.Bookmark, request.Oldest)
	if !errors.Is(err, ErrEventLogQueryComplex) {
		return reader, err
	}
	s.logger.Warn("Event log query is too complex; falling back to provider filtering", "channel", request.Channel)
	return s.backend.Open(ctx, request.Channel, eventLogQuery(request.Channel, request.Filters, true), request.Bookmark, request.Oldest)
}

func eventLogQuery(channel string, filters []EventLogFilter, providersOnly bool) string {
	type selection struct {
		all bool
		ids map[uint16]bool
	}
	byProvider := map[string]*selection{}
	for _, filter := range filters {
		entry := byProvider[filter.Provider]
		if entry == nil {
			entry = &selection{ids: map[uint16]bool{}}
			byProvider[filter.Provider] = entry
		}
		entry.all = entry.all || len(filter.IDs) == 0 || providersOnly
		for _, id := range filter.IDs {
			entry.ids[id] = true
		}
	}
	if len(byProvider) == 0 {
		return "*"
	}
	providers := make([]string, 0, len(byProvider))
	for provider := range byProvider {
		providers = append(providers, provider)
	}
	sort.Strings(providers)
	var expressions []string
	for _, provider := range providers {
		quote := "'"
		if strings.Contains(provider, quote) {
			quote = "\""
			if strings.Contains(provider, quote) {
				// The restricted Windows XPath dialect has no safe literal for both quote kinds.
				return "*"
			}
		}
		condition := "Provider[@Name=" + quote + provider + quote + "]"
		entry := byProvider[provider]
		if entry.all {
			expressions = append(expressions, condition)
			continue
		}
		var ids []int
		for id := range entry.ids {
			ids = append(ids, int(id))
		}
		slices.Sort(ids)
		for len(ids) > 0 {
			count := min(16, len(ids))
			values := make([]string, count)
			for index, id := range ids[:count] {
				values[index] = "EventID=" + strconv.Itoa(id)
			}
			expressions = append(expressions, condition+" and ("+strings.Join(values, " or ")+")")
			ids = ids[count:]
		}
	}
	// Each Select stays below Windows' small XPath expression limit.
	var result bytes.Buffer
	result.WriteString(`<QueryList><Query Id="0" Path="` + channel + `">`)
	for _, expression := range expressions {
		result.WriteString(`<Select Path="` + channel + `">`)
		result.WriteString(strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;").Replace("*[System[" + expression + "]]"))
		result.WriteString(`</Select>`)
	}
	result.WriteString(`</Query></QueryList>`)
	return result.String()
}

func (e EventLogRecord) Validate() error {
	if !EventLogChannel(e.Channel) || e.Provider == "" || e.EventID > math.MaxUint16 || e.RecordID == 0 || e.RecordID > math.MaxInt64 || e.TS <= 0 {
		return fmt.Errorf("事件日志的日志、来源、事件 ID、记录号或时间无效")
	}
	if !utf8.ValidString(e.Message) || len(e.Message) > EventLogMessageBytes {
		return fmt.Errorf("事件日志消息须为不超过 2 KiB 的 UTF-8 文本")
	}
	if len(e.Targets) > 32 {
		return fmt.Errorf("每条事件日志最多归属 32 个目标")
	}
	seen := map[string]bool{}
	for _, target := range e.Targets {
		if target.ID == "" || seen[target.ID] || target.Via != "subject" && target.Via != "subscription" && target.Via != "both" {
			return fmt.Errorf("事件日志目标 ID 或归属无效，且目标不能重复")
		}
		seen[target.ID] = true
	}
	seen = map[string]bool{}
	expected := EventLogPresets(e)
	for _, preset := range e.Presets {
		if seen[preset] || !slices.Contains(expected, preset) {
			return fmt.Errorf("事件日志预设与来源或事件 ID 不符，且预设不能重复")
		}
		seen[preset] = true
	}
	return nil
}

func eventLogText(value string) string {
	value = strings.ToValidUTF8(strings.TrimSpace(value), "�")
	if len(value) <= EventLogMessageBytes {
		return value
	}
	end := EventLogMessageBytes
	for !utf8.RuneStart(value[end]) {
		end--
	}
	return value[:end]
}

type eventLogXML struct {
	System struct {
		Provider struct {
			Name string `xml:"Name,attr"`
		} `xml:"Provider"`
		EventID  uint32 `xml:"EventID"`
		Level    uint8  `xml:"Level"`
		RecordID uint64 `xml:"EventRecordID"`
		Channel  string `xml:"Channel"`
		Time     struct {
			Value string `xml:"SystemTime,attr"`
		} `xml:"TimeCreated"`
	} `xml:"System"`
	Data []struct {
		Name  string `xml:"Name,attr"`
		Value string `xml:",chardata"`
	} `xml:"EventData>Data"`
}

func parseEventLogXML(channel, source, formatted string) (EventLogRecord, error) {
	var decoded eventLogXML
	if err := xml.Unmarshal([]byte(source), &decoded); err != nil {
		return EventLogRecord{}, fmt.Errorf("解析事件日志 XML 失败：%w", err)
	}
	at, err := time.Parse(time.RFC3339Nano, decoded.System.Time.Value)
	if err != nil {
		return EventLogRecord{}, fmt.Errorf("解析事件发生时间失败：%w", err)
	}
	if decoded.System.Channel != channel {
		return EventLogRecord{}, fmt.Errorf("事件日志与订阅的日志不一致")
	}
	row := EventLogRecord{Channel: channel, Provider: decoded.System.Provider.Name, EventID: decoded.System.EventID,
		Level: decoded.System.Level, RecordID: decoded.System.RecordID, TS: at.UnixMilli()}
	var values []string
	for _, data := range decoded.Data {
		if value := strings.TrimSpace(data.Value); value != "" {
			values = append(values, value)
		}
	}
	if channel == "Application" && (row.EventID == 1000 || row.EventID == 1002) && (row.Provider == "Application Error" || row.Provider == "Application Hang") {
		for _, data := range decoded.Data {
			if strings.EqualFold(data.Name, "AppName") || strings.EqualFold(data.Name, "ApplicationName") || strings.EqualFold(data.Name, "FaultingApplicationName") {
				row.Subject = data.Value
				break
			}
		}
		if row.Subject == "" && len(values) != 0 {
			row.Subject = values[0]
		}
		row.Subject = path.Base(strings.ReplaceAll(strings.TrimSpace(row.Subject), "\\", "/"))
		if row.Subject == "." {
			row.Subject = ""
		}
	} else if channel == "System" && row.Provider == "Service Control Manager" && (row.EventID == 7031 || row.EventID == 7034) {
		for _, data := range decoded.Data {
			if strings.EqualFold(data.Name, "ServiceName") || strings.EqualFold(data.Name, "param1") {
				row.Subject = strings.TrimSpace(data.Value)
				break
			}
		}
		if row.Subject == "" && len(values) != 0 {
			row.Subject = values[0]
		}
	}
	if strings.TrimSpace(formatted) == "" {
		formatted = strings.Join(values, "\n")
	}
	row.Message = eventLogText(formatted)
	return row, row.Validate()
}
