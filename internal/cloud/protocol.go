// Copyright (c) 2026 LFen (https://lfen.ink) — lg-monitor

// Package cloud implements optional email-authenticated multi-device synchronization using
// an ultra-lean 3-layer delta protocol (metadata hash, compact status frame, on-demand docs).
package cloud

import (
	"cmp"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"math"
	"net"
	"net/url"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/LFenX/lg-monitor/internal/api"
	"github.com/LFenX/lg-monitor/internal/jobs"
)

const (
	maxDescRunes    = 240
	maxIssueRunes   = 160
	beatCount       = 48
	beatSlotMS      = 30 * 60 * 1000
	detailSlotMS    = 15 * 60 * 1000
	maxSyncSegments = 192
	// MaxJobSnapshotBytes keeps one batch job's synced 30-day payload below the worker's 512 KiB row cap.
	MaxJobSnapshotBytes = 460 * 1024
)

// CompactBoard is the layer-1 metadata for one board.
type CompactBoard struct {
	ID        string            `json:"id"`
	Name      string            `json:"name"`
	Desc      string            `json:"desc,omitempty"`
	Monitors  []string          `json:"m"`
	CardStyle string            `json:"cs,omitempty"`
	Cards     map[string]string `json:"cards,omitempty"`
}

// CompactDep encodes [targetId, relation, optional(0|1), note] as a short JSON array.
type CompactDep [4]any

// CompactMonitorMeta is the layer-1 metadata for one monitor.
type CompactMonitorMeta struct {
	ID       string       `json:"id"`
	Name     string       `json:"n"`
	Parent   string       `json:"p,omitempty"`
	Kind     string       `json:"k,omitempty"`
	Role     string       `json:"r,omitempty"`
	Watching []string     `json:"w,omitempty"`
	Desc     string       `json:"d,omitempty"`
	Entry    string       `json:"e,omitempty"`
	Deps     []CompactDep `json:"dep,omitempty"`
	// Ext names what the service uses on the account's other devices as [device, service, relation], so every
	// device can draw the account's cross-device relations, not only its own.
	Ext     [][3]string `json:"x,omitempty"`
	DocHash string      `json:"dh,omitempty"`
}

// CompactMeta is uploaded only when its hash changes.
type CompactMeta struct {
	Boards   []CompactBoard       `json:"boards"`
	Monitors []CompactMonitorMeta `json:"monitors"`
}

// CompactJobBrief carries a batch job's latest meaningful day summary on board rows.
type CompactJobBrief struct {
	Date    string `json:"dt"`
	Status  string `json:"st"`
	Summary string `json:"sm"`
}

// CompactMonitorState is the layer-2 ultra-compact status frame for one monitor.
type CompactMonitorState struct {
	State     int              `json:"s"`            // 0=unknown, 1=up, 2=down
	Uptime    int              `json:"u"`            // basis points 0..10000, -1=unknown
	Latency   int              `json:"l"`            // ms rounded, -1=unknown
	Firing    int              `json:"f,omitempty"`  // firing alert count
	Issue     string           `json:"i,omitempty"`  // short issue text, omitted when up
	Beats     string           `json:"b,omitempty"`  // 48-char strip: '0'=empty, '1'=up, '2'=down, '3'=unknown
	BeatStart int64            `json:"bs,omitempty"` // UTC ms start of the 48-slot 24h window for Segments
	Segments  [][3]int64       `json:"sg,omitempty"` // merged 24h timeline runs: [fromOffsetMS, toOffsetMS, stateCode]
	Job       *CompactJobBrief `json:"j,omitempty"`  // latest batch job brief for kind="job"
	JobHash   string           `json:"jh,omitempty"` // 16-char hash of 30-day JobDays snapshot
}

// CompactState carries all monitors' latest compact status for a device.
type CompactState struct {
	TS       int64                          `json:"ts"`
	Monitors map[string]CompactMonitorState `json:"m"`
	// Devices is the latest resource sample of the computers this device monitors. It changes with every
	// sample, so it only rides along when the state is sent anyway, at most every stateRefreshEvery.
	Devices []api.Device `json:"dv,omitempty"`
}

type DeviceMetaInput struct {
	ID      string `json:"id,omitempty"`
	Name    string `json:"name,omitempty"`
	Avatar  string `json:"avatar,omitempty"`
	OS      string `json:"os,omitempty"`
	Version string `json:"version,omitempty"`
	// Machine is a hash that stays the same for this computer, so signing in again finds its earlier record.
	Machine string `json:"machine,omitempty"`
}

type SyncPushRequest struct {
	Device    *DeviceMetaInput `json:"device,omitempty"`
	MetaHash  string           `json:"metaHash,omitempty"`
	StateHash string           `json:"stateHash,omitempty"`
	Meta      *CompactMeta     `json:"meta,omitempty"`
	State     *CompactState    `json:"state,omitempty"`
}

type SyncPushResponse struct {
	OK         bool     `json:"ok"`
	Seq        int64    `json:"seq"`
	NeedMeta   bool     `json:"needMeta"`
	NeedState  bool     `json:"needState"`
	NeedDocs   []string `json:"needDocs"`
	NeedJobs   []string `json:"needJobs"`
	ServerTime int64    `json:"serverTime"`
}

type SyncDocItem struct {
	MonitorID string `json:"monitorId"`
	Hash      string `json:"hash"`
	Markdown  string `json:"markdown"`
}

type SyncDocsRequest struct {
	Docs []SyncDocItem `json:"docs"`
}

type SyncJobItem struct {
	MonitorID string      `json:"monitorId"`
	Hash      string      `json:"hash"`
	Payload   api.JobDays `json:"payload"`
}

type SyncJobsRequest struct {
	Jobs []SyncJobItem `json:"jobs"`
}

type RemoteDevicePayload struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Avatar    string `json:"avatar"`
	OS        string `json:"os"`
	Version   string `json:"version"`
	IsCurrent bool   `json:"isCurrent"`
	// SameMachine marks another record that this same computer left in the account.
	SameMachine bool          `json:"sameMachine,omitempty"`
	Online      bool          `json:"online"`
	LastSeen    int64         `json:"lastSeen"`
	Seq         int64         `json:"seq"`
	MetaHash    string        `json:"metaHash"`
	StateHash   string        `json:"stateHash"`
	Meta        *CompactMeta  `json:"meta,omitempty"`
	State       *CompactState `json:"state,omitempty"`
}

type SyncSnapshotResponse struct {
	Seq        int64                 `json:"seq"`
	ServerTime int64                 `json:"serverTime"`
	Devices    []RemoteDevicePayload `json:"devices"`
}

type SyncDocResponse struct {
	DeviceID  string `json:"deviceId"`
	MonitorID string `json:"monitorId"`
	Hash      string `json:"hash"`
	Markdown  string `json:"markdown"`
	UpdatedAt int64  `json:"updatedAt"`
}

type SyncJobResponse struct {
	DeviceID  string      `json:"deviceId"`
	MonitorID string      `json:"monitorId"`
	Hash      string      `json:"hash"`
	Payload   api.JobDays `json:"payload"`
	UpdatedAt int64       `json:"updatedAt"`
}

func truncateRunes(s string, max int) string {
	s = strings.TrimSpace(s)
	if utf8.RuneCountInString(s) <= max {
		return s
	}
	runes := []rune(s)
	return string(runes[:max])
}

// StateCode converts lgmon state strings to compact ints.
func StateCode(state string) int {
	switch state {
	case "up":
		return 1
	case "down":
		return 2
	default:
		return 0
	}
}

// DecodeStateCode converts compact ints back to lgmon state strings.
func DecodeStateCode(code int) string {
	switch code {
	case 1:
		return "up"
	case 2:
		return "down"
	default:
		return "unknown"
	}
}

// EncodeUptime encodes percentage [0..100] as basis points [0..10000], or -1 when nil.
func EncodeUptime(v *float64) int {
	if v == nil || math.IsNaN(*v) || math.IsInf(*v, 0) {
		return -1
	}
	bp := int(math.Round(*v * 100))
	if bp < 0 {
		return 0
	}
	if bp > 10000 {
		return 10000
	}
	return bp
}

// DecodeUptime converts basis points back to percentage pointer.
func DecodeUptime(bp int) *float64 {
	if bp < 0 {
		return nil
	}
	v := float64(bp) / 100.0
	return &v
}

// EncodeLatency rounds milliseconds to an int, or -1 when nil.
func EncodeLatency(v *float64) int {
	if v == nil || math.IsNaN(*v) || math.IsInf(*v, 0) {
		return -1
	}
	ms := int(math.Round(*v))
	if ms < 0 {
		return 0
	}
	return ms
}

// DecodeLatency converts rounded ms back to float64 pointer.
func DecodeLatency(ms int) *float64 {
	if ms < 0 {
		return nil
	}
	v := float64(ms)
	return &v
}

// EncodeBeats packs a 48-slot beat slice into a 48-byte string ('0'=empty,'1'=up,'2'=down,'3'=unknown).
func EncodeBeats(beats []string) string {
	if len(beats) == 0 {
		return ""
	}
	buf := make([]byte, beatCount)
	start := 0
	if len(beats) > beatCount {
		beats = beats[len(beats)-beatCount:]
	} else if len(beats) < beatCount {
		start = beatCount - len(beats)
		for i := 0; i < start; i++ {
			buf[i] = '0'
		}
	}
	hasNonEmpty := false
	for i, b := range beats {
		var c byte = '0'
		switch b {
		case "up":
			c = '1'
			hasNonEmpty = true
		case "down":
			c = '2'
			hasNonEmpty = true
		case "unknown":
			c = '3'
			hasNonEmpty = true
		}
		buf[start+i] = c
	}
	if !hasNonEmpty {
		return ""
	}
	return string(buf)
}

// DecodeBeats expands a compact beat string back into a 48-element beat slice.
func DecodeBeats(encoded string) []string {
	out := make([]string, beatCount)
	if encoded == "" {
		return out
	}
	offset := 0
	if len(encoded) < beatCount {
		offset = beatCount - len(encoded)
	} else if len(encoded) > beatCount {
		encoded = encoded[len(encoded)-beatCount:]
	}
	for i := 0; i < len(encoded); i++ {
		switch encoded[i] {
		case '1':
			out[offset+i] = "up"
		case '2':
			out[offset+i] = "down"
		case '3':
			out[offset+i] = "unknown"
		default:
			out[offset+i] = ""
		}
	}
	return out
}

// EncodeSegments extracts and merges exact timed segments from 24h BeatSlots into compact
// [fromOffsetMS, toOffsetMS, stateCode] tuples relative to beatStart.
func EncodeSegments(slots []api.StatusSlot) (int64, [][3]int64) {
	if len(slots) == 0 || slots[0].From <= 0 {
		return 0, nil
	}
	beatStart := slots[0].From
	raw := make([]api.StatusSegment, 0, len(slots))
	for _, slot := range slots {
		for _, seg := range slot.Segments {
			if seg.Summary || seg.To < seg.From {
				continue
			}
			if seg.State != "up" && seg.State != "down" && seg.State != "unknown" {
				continue
			}
			raw = append(raw, seg)
		}
	}
	if len(raw) == 0 {
		return 0, nil
	}
	slices.SortFunc(raw, func(a, b api.StatusSegment) int {
		if c := cmp.Compare(a.From, b.From); c != 0 {
			return c
		}
		if c := cmp.Compare(a.To, b.To); c != 0 {
			return c
		}
		return cmp.Compare(StateCode(a.State), StateCode(b.State))
	})
	merged := make([][3]int64, 0, len(raw))
	for _, seg := range raw {
		fromOff := max(int64(0), seg.From-beatStart)
		toOff := max(fromOff, seg.To-beatStart)
		code := int64(StateCode(seg.State))
		if n := len(merged); n > 0 && merged[n-1][2] == code && fromOff <= merged[n-1][1] {
			if toOff > merged[n-1][1] {
				merged[n-1][1] = toOff
			}
			continue
		}
		merged = append(merged, [3]int64{fromOff, toOff, code})
	}
	if len(merged) > maxSyncSegments {
		merged = merged[len(merged)-maxSyncSegments:]
	}
	return beatStart, merged
}

// DecodeSegments expands compact [fromOffsetMS, toOffsetMS, stateCode] tuples back into absolute StatusSegments.
func DecodeSegments(beatStart int64, encoded [][3]int64) []api.StatusSegment {
	if beatStart <= 0 || len(encoded) == 0 {
		return nil
	}
	out := make([]api.StatusSegment, 0, len(encoded))
	for _, item := range encoded {
		if item[1] < item[0] {
			continue
		}
		out = append(out, api.StatusSegment{
			From:  beatStart + item[0],
			To:    beatStart + item[1],
			State: DecodeStateCode(int(item[2])),
		})
	}
	return out
}

func buildSlotsFromSegments(beatStart int64, count int, spanMS int64, segs []api.StatusSegment) []api.StatusSlot {
	slots := make([]api.StatusSlot, count)
	windowEnd := beatStart + int64(count)*spanMS
	for i := 0; i < count; i++ {
		slotFrom := beatStart + int64(i)*spanMS
		slotTo := slotFrom + spanMS
		slot := api.StatusSlot{From: slotFrom}
		var upMS, knownMS int64
		for _, seg := range segs {
			if seg.To < slotFrom || (seg.From >= slotTo && !(i == count-1 && seg.From == windowEnd && slotTo == windowEnd)) {
				continue
			}
			if seg.To == slotFrom && seg.From != seg.To {
				continue
			}
			cFrom := max(seg.From, slotFrom)
			cTo := min(seg.To, slotTo)
			slot.Segments = append(slot.Segments, api.StatusSegment{
				From:  cFrom,
				To:    cTo,
				State: seg.State,
			})
			slot.State = worseState(slot.State, seg.State)
			dur := cTo - cFrom
			switch seg.State {
			case "up":
				upMS += dur
				knownMS += dur
			case "down":
				knownMS += dur
			}
		}
		if knownMS > 0 {
			pct := math.Round((float64(upMS)*100/float64(knownMS))*100) / 100
			slot.Uptime = &pct
		}
		slots[i] = slot
	}
	return slots
}

// buildLegacySummarySlots turns 48 half-hour beat summaries from older devices into summary-only slots
// without fabricating 0% uptime or full-period outage intervals.
func buildLegacySummarySlots(startMS int64, beats []string) []api.StatusSlot {
	slots := make([]api.StatusSlot, beatCount)
	for i := 0; i < beatCount; i++ {
		from := startMS + int64(i)*beatSlotMS
		beat := ""
		if i < len(beats) {
			beat = beats[i]
		}
		slot := api.StatusSlot{From: from, State: beat}
		if beat != "" {
			slot.Segments = []api.StatusSegment{{
				From:    from,
				To:      from + beatSlotMS,
				State:   beat,
				Summary: true,
			}}
		}
		slots[i] = slot
	}
	return slots
}

func hasAnyBeat(beats []string) bool {
	for _, b := range beats {
		if b != "" {
			return true
		}
	}
	return false
}

// HashDoc computes a short deterministic hash of a Markdown document.
func HashDoc(markdown string) string {
	trimmed := strings.TrimSpace(markdown)
	if trimmed == "" {
		return ""
	}
	sum := sha256.Sum256([]byte(trimmed))
	return hex.EncodeToString(sum[:8])
}

// HashJSON returns a deterministic 16-char SHA-256 hex digest of a JSON-serializable struct.
func HashJSON(v any) string {
	raw, err := json.Marshal(v)
	if err != nil {
		return ""
	}
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:8])
}

// shareableEntry drops loopback addresses: on another device they would open that device's own port.
func shareableEntry(entry string) string {
	parsed, err := url.Parse(strings.TrimSpace(entry))
	if err != nil || parsed.Host == "" {
		return entry
	}
	host := parsed.Hostname()
	if strings.EqualFold(host, "localhost") {
		return ""
	}
	if ip := net.ParseIP(host); ip != nil && ip.IsLoopback() {
		return ""
	}
	return entry
}

// TrimJobDaysForSync keeps a batch job's 30-day payload within maxBytes by dropping hourly partitions
// from the oldest days first (preserving recent days and every day's status/stage timeline).
func TrimJobDaysForSync(snap api.JobDays, maxBytes int) api.JobDays {
	if maxBytes <= 0 {
		maxBytes = MaxJobSnapshotBytes
	}
	if raw, err := json.Marshal(snap); err == nil && len(raw) <= maxBytes {
		return snap
	}
	trimmed := api.JobDays{Job: snap.Job, Days: make([]jobs.Day, len(snap.Days))}
	for i, d := range snap.Days {
		dc := d
		if len(d.Attempts) > 0 {
			dc.Attempts = make([]jobs.Attempt, len(d.Attempts))
			copy(dc.Attempts, d.Attempts)
		}
		trimmed.Days[i] = dc
	}
	// Pass 1: drop hourly partitions from oldest days first, keeping the newest day intact if possible.
	for i := len(trimmed.Days) - 1; i >= 1; i-- {
		changed := false
		for j := range trimmed.Days[i].Attempts {
			if len(trimmed.Days[i].Attempts[j].Partitions) > 0 {
				trimmed.Days[i].Attempts[j].Partitions = []jobs.Partition{}
				changed = true
			}
		}
		if !changed {
			continue
		}
		if raw, err := json.Marshal(trimmed); err == nil && len(raw) <= maxBytes {
			return trimmed
		}
	}
	// Pass 2: trim oldest days if still over budget.
	for len(trimmed.Days) > 1 {
		trimmed.Days = trimmed.Days[:len(trimmed.Days)-1]
		if raw, err := json.Marshal(trimmed); err == nil && len(raw) <= maxBytes {
			return trimmed
		}
	}
	return trimmed
}

// HashJobDays returns a deterministic 16-char hash for a synced batch job snapshot.
func HashJobDays(snap api.JobDays) string {
	return HashJSON(TrimJobDaysForSync(snap, MaxJobSnapshotBytes))
}

// CriticalStateHash covers only what someone would act on: up/down, firing alerts, issue text,
// and batch job daily record updates. A change here is pushed at once; everything else may wait.
func CriticalStateHash(state CompactState) string {
	type critical struct {
		S  int              `json:"s"`
		F  int              `json:"f,omitempty"`
		I  string           `json:"i,omitempty"`
		J  *CompactJobBrief `json:"j,omitempty"`
		JH string           `json:"jh,omitempty"`
	}
	view := make(map[string]critical, len(state.Monitors))
	for id, st := range state.Monitors {
		view[id] = critical{S: st.State, F: st.Firing, I: st.Issue, J: st.Job, JH: st.JobHash}
	}
	return HashJSON(view)
}

// BuildCompactSnapshot constructs layer-1 metadata and layer-2 state payloads with stable hashes.
// State hash intentionally excludes the moving timestamp so unchanged monitoring states produce
// identical hashes and emit tiny heartbeat-only pushes.
func BuildCompactSnapshot(boards []api.BoardInfo, monitors []api.Monitor, docHashes map[string]string, nowMS int64) (CompactMeta, string, CompactState, string) {
	return BuildCompactSnapshotWithJobs(boards, monitors, docHashes, nil, nowMS)
}

// BuildCompactSnapshotWithJobs is BuildCompactSnapshot with optional batch job content hashes.
func BuildCompactSnapshotWithJobs(boards []api.BoardInfo, monitors []api.Monitor, docHashes, jobHashes map[string]string, nowMS int64) (CompactMeta, string, CompactState, string) {
	cBoards := make([]CompactBoard, 0, len(boards))
	for _, b := range boards {
		cb := CompactBoard{
			ID:        b.ID,
			Name:      truncateRunes(b.Name, 40),
			Desc:      truncateRunes(b.Description, 200),
			Monitors:  append([]string{}, b.Monitors...),
			CardStyle: b.CardStyle,
		}
		if len(b.Cards) > 0 {
			cb.Cards = b.Cards
		}
		cBoards = append(cBoards, cb)
	}

	sortedMonitors := slices.Clone(monitors)
	slices.SortFunc(sortedMonitors, func(a, b api.Monitor) int { return strings.Compare(a.ID, b.ID) })

	cMonitors := make([]CompactMonitorMeta, 0, len(sortedMonitors))
	stateMap := make(map[string]CompactMonitorState, len(sortedMonitors))

	for _, m := range sortedMonitors {
		cm := CompactMonitorMeta{
			ID:       m.ID,
			Name:     truncateRunes(m.Name, 80),
			Parent:   m.Parent,
			Kind:     m.Kind,
			Role:     m.Role,
			Watching: m.Watching,
			Desc:     truncateRunes(m.Description, maxDescRunes),
			Entry:    shareableEntry(m.Entry),
			DocHash:  docHashes[m.ID],
		}
		if len(m.Depends) > 0 {
			deps := make([]CompactDep, 0, len(m.Depends))
			for _, d := range m.Depends {
				opt := 0
				if d.Optional {
					opt = 1
				}
				deps = append(deps, CompactDep{d.ID, d.Relation, opt, truncateRunes(d.Note, 80)})
			}
			cm.Deps = deps
		}
		for _, link := range m.External {
			if link.Device != "" && link.ID != "" && len(cm.Ext) < 32 {
				cm.Ext = append(cm.Ext, [3]string{link.Device, link.ID, link.Relation})
			}
		}
		cMonitors = append(cMonitors, cm)

		issue := ""
		if m.State != "up" {
			issue = truncateRunes(m.Issue, maxIssueRunes)
		}
		var jobBrief *CompactJobBrief
		if m.Job != nil {
			jobBrief = &CompactJobBrief{
				Date:    m.Job.Date,
				Status:  string(m.Job.Status),
				Summary: truncateRunes(m.Job.Summary, maxIssueRunes),
			}
		}
		beatStart, segments := EncodeSegments(m.BeatSlots)
		stateMap[m.ID] = CompactMonitorState{
			State:     StateCode(m.State),
			Uptime:    EncodeUptime(m.Uptime),
			Latency:   EncodeLatency(m.LatencyMS),
			Firing:    m.Firing,
			Issue:     issue,
			Beats:     EncodeBeats(m.Beats),
			BeatStart: beatStart,
			Segments:  segments,
			Job:       jobBrief,
			JobHash:   jobHashes[m.ID],
		}
	}

	meta := CompactMeta{Boards: cBoards, Monitors: cMonitors}
	metaHash := HashJSON(meta)
	// Hash only stateMap so identical monitoring health never triggers redundant state uploads.
	stateHash := HashJSON(stateMap)
	state := CompactState{TS: nowMS, Monitors: stateMap}
	return meta, metaHash, state, stateHash
}

// Reconstructed types exposed to local API consumers.

type DeviceSummary struct {
	ID           string          `json:"id"`
	Name         string          `json:"name"`
	Avatar       string          `json:"avatar,omitempty"`
	OS           string          `json:"os,omitempty"`
	Version      string          `json:"version,omitempty"`
	IsCurrent    bool            `json:"isCurrent"`
	SameMachine  bool            `json:"sameMachine,omitempty"`
	Online       bool            `json:"online"`
	LastSeen     int64           `json:"lastSeen"`
	StateUpdated int64           `json:"stateUpdated,omitempty"`
	TotalCount   int             `json:"totalCount"`
	DownCount    int             `json:"downCount"`
	Boards       []api.BoardInfo `json:"boards"`
}

type SyncedBoardView struct {
	DeviceID     string        `json:"deviceId"`
	DeviceName   string        `json:"deviceName"`
	DeviceAvatar string        `json:"deviceAvatar,omitempty"`
	DeviceOS     string        `json:"deviceOs,omitempty"`
	Online       bool          `json:"online"`
	LastSeen     int64         `json:"lastSeen"`
	StateUpdated int64         `json:"stateUpdated,omitempty"`
	Board        api.BoardView `json:"board"`
}

type reconstructedDevice struct {
	Summary   DeviceSummary
	Boards    map[string]api.BoardView
	Monitors  map[string]api.Monitor
	DocHash   map[string]string
	JobHash   map[string]string
	BeatStart map[string]int64
	Segments  map[string][]api.StatusSegment
}

func worseState(a, b string) string {
	rank := map[string]int{"": 0, "up": 1, "unknown": 2, "down": 3}
	if rank[b] > rank[a] {
		return b
	}
	return a
}

// ReconstructDevice rebuilds full lgmon BoardInfo, BoardView, and Monitor trees from compact wire data.
func ReconstructDevice(dev RemoteDevicePayload) reconstructedDevice {
	result := reconstructedDevice{
		Summary: DeviceSummary{
			ID:          dev.ID,
			Name:        dev.Name,
			Avatar:      dev.Avatar,
			OS:          dev.OS,
			Version:     dev.Version,
			IsCurrent:   dev.IsCurrent,
			SameMachine: dev.SameMachine,
			Online:      dev.Online,
			LastSeen:    dev.LastSeen,
			Boards:      []api.BoardInfo{},
		},
		Boards:    map[string]api.BoardView{},
		Monitors:  map[string]api.Monitor{},
		DocHash:   map[string]string{},
		JobHash:   map[string]string{},
		BeatStart: map[string]int64{},
		Segments:  map[string][]api.StatusSegment{},
	}
	if dev.State != nil {
		result.Summary.StateUpdated = dev.State.TS
	}
	if dev.Meta == nil {
		return result
	}

	childrenMap := map[string][]string{}
	metaByID := make(map[string]CompactMonitorMeta, len(dev.Meta.Monitors))
	for _, cm := range dev.Meta.Monitors {
		metaByID[cm.ID] = cm
		if cm.Parent != "" {
			childrenMap[cm.Parent] = append(childrenMap[cm.Parent], cm.ID)
		}
		if cm.DocHash != "" {
			result.DocHash[cm.ID] = cm.DocHash
		}
	}

	listedBoards := map[string][]string{}
	for _, cb := range dev.Meta.Boards {
		for _, mid := range cb.Monitors {
			listedBoards[mid] = append(listedBoards[mid], cb.ID)
		}
	}

	dependentsMap := map[string][]api.MonitorDependency{}
	for _, cm := range dev.Meta.Monitors {
		for _, dep := range cm.Deps {
			targetID, _ := dep[0].(string)
			rel, _ := dep[1].(string)
			opt := false
			switch v := dep[2].(type) {
			case float64:
				opt = v != 0
			case int:
				opt = v != 0
			case bool:
				opt = v
			}
			note, _ := dep[3].(string)
			if targetID != "" {
				dependentsMap[targetID] = append(dependentsMap[targetID], api.MonitorDependency{
					ID:       cm.ID,
					Relation: rel,
					Optional: opt,
					Note:     note,
				})
			}
		}
	}

	depthOf := func(id string) int {
		depth := 0
		seen := map[string]bool{id: true}
		for cur := metaByID[id].Parent; cur != "" && !seen[cur]; cur = metaByID[cur].Parent {
			if _, ok := metaByID[cur]; !ok {
				break
			}
			seen[cur] = true
			depth++
		}
		return depth
	}

	var stateMap map[string]CompactMonitorState
	if dev.State != nil {
		stateMap = dev.State.Monitors
	}

	for _, cm := range dev.Meta.Monitors {
		st, hasState := stateMap[cm.ID]
		stateStr := "unknown"
		var uptime *float64
		var latency *float64
		var beats []string
		var beatSlots []api.StatusSlot
		var jobBrief *api.JobBrief
		issue := ""
		firing := 0
		if hasState {
			stateStr = DecodeStateCode(st.State)
			uptime = DecodeUptime(st.Uptime)
			latency = DecodeLatency(st.Latency)
			beats = DecodeBeats(st.Beats)
			issue = st.Issue
			firing = st.Firing
			if segs := DecodeSegments(st.BeatStart, st.Segments); len(segs) > 0 {
				result.BeatStart[cm.ID] = st.BeatStart
				result.Segments[cm.ID] = segs
				beatSlots = buildSlotsFromSegments(st.BeatStart, beatCount, beatSlotMS, segs)
			} else if st.Beats != "" && dev.State != nil && dev.State.TS > 0 {
				beatSlots = buildLegacySummarySlots(dev.State.TS-int64(beatCount)*beatSlotMS, beats)
			}
			if st.Job != nil {
				jobBrief = &api.JobBrief{
					Date:    st.Job.Date,
					Status:  jobs.Status(st.Job.Status),
					Summary: st.Job.Summary,
				}
			}
			if st.JobHash != "" {
				result.JobHash[cm.ID] = st.JobHash
			}
		} else {
			beats = make([]string, beatCount)
		}
		deps := make([]api.MonitorDependency, 0, len(cm.Deps))
		for _, dep := range cm.Deps {
			targetID, _ := dep[0].(string)
			rel, _ := dep[1].(string)
			opt := false
			switch v := dep[2].(type) {
			case float64:
				opt = v != 0
			case int:
				opt = v != 0
			case bool:
				opt = v
			}
			note, _ := dep[3].(string)
			if targetID != "" {
				deps = append(deps, api.MonitorDependency{ID: targetID, Relation: rel, Optional: opt, Note: note})
			}
		}
		mon := api.Monitor{
			ID:          cm.ID,
			Name:        cm.Name,
			Watching:    append([]string{}, cm.Watching...),
			State:       stateStr,
			Overall:     stateStr,
			Issue:       issue,
			Hosts:       []api.MonitorHost{},
			Rules:       []api.MonitorRule{},
			Firing:      firing,
			Beats:       beats,
			BeatSlots:   beatSlots,
			Uptime:      uptime,
			LatencyMS:   latency,
			Kind:        cm.Kind,
			Role:        cm.Role,
			Description: cm.Desc,
			Entry:       cm.Entry,
			Parent:      cm.Parent,
			Children:    append([]string{}, childrenMap[cm.ID]...),
			Depth:       depthOf(cm.ID),
			Boards:      append([]string{}, listedBoards[cm.ID]...),
			Depends:     deps,
			External:    externalLinks(cm.Ext),
			Dependents:  dependentsMap[cm.ID],
			HasDoc:      cm.DocHash != "",
			Job:         jobBrief,
		}
		result.Monitors[cm.ID] = mon
		result.Summary.TotalCount++
		if mon.State == "down" {
			result.Summary.DownCount++
		}
	}

	// Build BoardViews and BoardInfo summaries.
	for _, cb := range dev.Meta.Boards {
		listed := map[string]bool{}
		for _, mid := range cb.Monitors {
			listed[mid] = true
		}
		shown := map[string]bool{}
		items := []api.Monitor{}
		var add func(id string, depth int)
		add = func(id string, depth int) {
			mon, ok := result.Monitors[id]
			if !ok || shown[id] {
				return
			}
			shown[id] = true
			mon.Depth = depth
			items = append(items, mon)
			for _, childID := range childrenMap[id] {
				add(childID, depth+1)
			}
		}
		for _, mid := range cb.Monitors {
			// Skip monitors whose ancestor is also on this board; they appear under that ancestor.
			hasListedAncestor := false
			seen := map[string]bool{mid: true}
			for cur := metaByID[mid].Parent; cur != "" && !seen[cur]; cur = metaByID[cur].Parent {
				seen[cur] = true
				if listed[cur] {
					hasListedAncestor = true
					break
				}
			}
			if !hasListedAncestor {
				add(mid, 0)
			}
		}
		summary := summarizeItems(items)
		info := api.BoardInfo{
			ID:          cb.ID,
			Name:        cb.Name,
			Description: cb.Desc,
			Monitors:    append([]string{}, cb.Monitors...),
			Summary:     summary,
			CardStyle:   cb.CardStyle,
			Cards:       cb.Cards,
		}
		result.Summary.Boards = append(result.Summary.Boards, info)
		result.Boards[cb.ID] = api.BoardView{
			BoardInfo:  info,
			Items:      items,
			Unassigned: []string{},
		}
	}

	return result
}

func summarizeItems(items []api.Monitor) api.BoardSummary {
	summary := api.BoardSummary{State: "empty"}
	if len(items) == 0 {
		return summary
	}
	var uptimeSum float64
	uptimeCount := 0
	for _, item := range items {
		if item.Depth == 0 {
			summary.Services++
		} else {
			summary.Children++
		}
		switch item.State {
		case "up":
			summary.Up++
		case "down":
			summary.Down++
		default:
			summary.Unknown++
		}
		if item.Uptime != nil {
			uptimeSum += *item.Uptime
			uptimeCount++
		}
	}
	if uptimeCount > 0 {
		avg := math.Round((uptimeSum/float64(uptimeCount))*100) / 100
		summary.Uptime = &avg
	}
	switch {
	case summary.Down > 0:
		summary.State = "down"
	case summary.Up > 0:
		summary.State = "up"
	default:
		summary.State = "unknown"
	}
	return summary
}

// ReconstructMonitorDetail builds a read-only MonitorDetail suitable for ServicePage from synced data.
func ReconstructMonitorDetail(dev reconstructedDevice, monitorID string, nowMS int64) (api.MonitorDetail, bool) {
	mon, ok := dev.Monitors[monitorID]
	if !ok {
		return api.MonitorDetail{}, false
	}

	// Build lineage from root down to direct parent.
	lineage := []api.MonitorRef{}
	seen := map[string]bool{monitorID: true}
	for cur := mon.Parent; cur != "" && !seen[cur]; cur = dev.Monitors[cur].Parent {
		parent, found := dev.Monitors[cur]
		if !found {
			break
		}
		seen[cur] = true
		lineage = append(lineage, api.MonitorRef{ID: parent.ID, Name: parent.Name, Overall: parent.Overall})
	}
	slices.Reverse(lineage)

	children := []api.Monitor{}
	for _, cid := range mon.Children {
		if child, found := dev.Monitors[cid]; found {
			children = append(children, child)
		}
	}

	boards := []api.BoardRef{}
	for _, b := range dev.Summary.Boards {
		if slices.Contains(mon.Boards, b.ID) {
			boards = append(boards, api.BoardRef{ID: b.ID, Name: b.Name})
		}
	}

	// Rebuild the 24h bar from exact synced segments when available. Older devices only upload
	// 48 half-hour beat summaries, so mark those as summary-only without fabricating 0% uptime.
	baseMS := dev.Summary.StateUpdated
	if baseMS == 0 {
		baseMS = nowMS
	}
	var slots []api.StatusSlot
	historyNotice := "云同步展示最近 24 小时状态摘要；详细历史与告警记录保存在原设备本机。"
	if segs := dev.Segments[monitorID]; len(segs) > 0 && dev.BeatStart[monitorID] > 0 {
		slots = buildSlotsFromSegments(dev.BeatStart[monitorID], beatCount*2, detailSlotMS, segs)
		historyNotice = "云同步展示最近 24 小时状态与精确异常时段；更早历史与告警记录保存在原设备本机。"
	} else if hasAnyBeat(mon.Beats) {
		startMS := baseMS - int64(beatCount)*beatSlotMS
		slots = buildLegacySummarySlots(startMS, mon.Beats)
		historyNotice = "旧设备仅上传最近 24 小时半小时摘要，异常格仅标明该时段曾有异常，未保留精确起止时间；升级原设备后可同步精确异常时段。"
	} else {
		startMS := baseMS - int64(beatCount)*beatSlotMS
		slots = make([]api.StatusSlot, beatCount*2)
		for i := range slots {
			slots[i] = api.StatusSlot{From: startMS + int64(i)*detailSlotMS}
		}
	}

	return api.MonitorDetail{
		Monitor:       mon,
		Lineage:       lineage,
		Children:      children,
		Boards:        boards,
		Slots:         slots,
		Days:          []api.StatusSlot{},
		Uptime24h:     mon.Uptime,
		Latency:       []api.LatencyPoint{},
		LatencyNow:    mon.LatencyMS,
		Events:        []api.MonitorEvent{},
		Incidents:     []api.MonitorIncident{},
		Rules:         []api.MonitorRuleDetail{},
		HistoryNotice: historyNotice,
	}, true
}

func externalLinks(ext [][3]string) []api.MonitorDependency {
	var links []api.MonitorDependency
	for _, item := range ext {
		if item[0] != "" && item[1] != "" {
			links = append(links, api.MonitorDependency{Device: item[0], ID: item[1], Relation: item[2]})
		}
	}
	return links
}
