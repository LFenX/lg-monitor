// Copyright (c) 2026 LFen (https://lfen.ink) — lg-monitor

package cloud

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/LFenX/lg-monitor/internal/api"
	"github.com/LFenX/lg-monitor/internal/jobs"
)

type fakeJobProvider struct {
	*fakeProvider
	jmu  sync.Mutex
	snap api.JobDays
}

func (p *fakeJobProvider) CloudJobHashes(_ context.Context) (map[string]string, error) {
	p.jmu.Lock()
	defer p.jmu.Unlock()
	return map[string]string{"aics-daily": HashJobDays(p.snap)}, nil
}

func (p *fakeJobProvider) CloudJobSnapshot(_ context.Context, id string) (api.JobDays, bool, error) {
	p.jmu.Lock()
	defer p.jmu.Unlock()
	if id != "aics-daily" {
		return api.JobDays{}, false, nil
	}
	return p.snap, true, nil
}

func sampleJobDays(summary string) api.JobDays {
	return api.JobDays{
		Job: api.JobInfo{ID: "aics-daily", Name: "AICS 每日上传", Source: "file", Production: []string{"prod-api"}},
		Days: []jobs.Day{{
			Date: "2026-11-21", Status: jobs.Success, Summary: summary, Enabled: true,
			Attempts: []jobs.Attempt{{
				ID: "run-1", Status: jobs.Success, Summary: summary, StartedAt: 1, Events: 65,
				Stages:     []jobs.StageView{{ID: "fetch", Name: "生产数据获取", State: "success"}},
				Partitions: []jobs.Partition{{Region: "cn", HourLabel: "2026/11/21 16 时（UTC+8）"}},
			}},
		}},
	}
}

func TestBatchJobRecordsSyncOnlyWhenChangedAndServeRemoteDetail(t *testing.T) {
	now := time.UnixMilli(1795350000000)
	clock := func() time.Time { return now }
	var (
		pushes    []SyncPushRequest
		uploads   []SyncJobItem
		jobGets   int
		storedJob = map[string]SyncJobItem{}
	)
	peerBrief := &CompactJobBrief{Date: "2026-11-21", Status: "success", Summary: "全部阶段成功"}
	peerSnap := sampleJobDays("全部阶段成功")
	peerHash := HashJobDays(peerSnap)
	client := &scriptedClient{handle: func(call scriptedCall) (int, any, string) {
		if status, body, etag, ok := signedInReply(call); ok {
			return status, body, etag
		}
		switch {
		case call.Path == "/v1/sync/push":
			var req SyncPushRequest
			_ = json.Unmarshal(call.Body, &req)
			pushes = append(pushes, req)
			need := []string{}
			if req.State != nil {
				for id, st := range req.State.Monitors {
					if st.JobHash != "" && storedJob[id].Hash != st.JobHash {
						need = append(need, id)
					}
				}
			}
			return 200, SyncPushResponse{OK: true, NeedJobs: need}, ""
		case call.Path == "/v1/sync/jobs":
			var req SyncJobsRequest
			_ = json.Unmarshal(call.Body, &req)
			for _, item := range req.Jobs {
				uploads = append(uploads, item)
				storedJob[item.MonitorID] = item
			}
			return 200, map[string]any{"ok": true}, ""
		case strings.HasPrefix(call.Path, "/v1/sync/snapshot"):
			return 200, SyncSnapshotResponse{Seq: 1, Devices: []RemoteDevicePayload{{
				ID: "dev-home", Name: "家里电脑", Online: true, Seq: 1, MetaHash: "m", StateHash: "s",
				Meta: &CompactMeta{
					Boards: []CompactBoard{{ID: "b", Name: "AICS", Monitors: []string{"aics-daily", "prod-api"}}},
					Monitors: []CompactMonitorMeta{
						{ID: "aics-daily", Name: "AICS 每日上传", Kind: "job"},
						{ID: "prod-api", Name: "生产接口", Kind: "url"},
					},
				},
				State: &CompactState{TS: now.UnixMilli(), Monitors: map[string]CompactMonitorState{
					"aics-daily": {State: 1, Uptime: -1, Latency: -1, Job: peerBrief, JobHash: peerHash},
					"prod-api":   {State: 2, Uptime: 9900, Latency: -1, Issue: "连接超时"},
				}},
			}}}, `"snap-1"`
		case call.Path == "/v1/sync/devices/dev-home/jobs/aics-daily":
			jobGets++
			if call.IfNoneMatch == `"`+peerHash+`"` {
				return 304, nil, call.IfNoneMatch
			}
			return 200, SyncJobResponse{DeviceID: "dev-home", MonitorID: "aics-daily", Hash: peerHash, Payload: peerSnap}, `"` + peerHash + `"`
		}
		return 404, nil, ""
	}}

	base := oneMonitor(10)
	base.monitors = append(base.monitors, api.Monitor{ID: "aics-daily", Name: "AICS 每日上传", Kind: "job", State: "up",
		Job: &api.JobBrief{Date: "2026-11-21", Status: jobs.Success, Summary: "全部阶段成功"}})
	provider := &fakeJobProvider{fakeProvider: base, snap: sampleJobDays("全部阶段成功")}
	mgr := newSignedIn(t, t.TempDir(), &fakeSecrets{}, client, clock, provider)
	ctx := context.Background()

	if err := mgr.PushOnce(ctx, true); err != nil {
		t.Fatalf("first push: %v", err)
	}
	st := pushes[0].State.Monitors["aics-daily"]
	if st.Job == nil || st.Job.Summary != "全部阶段成功" || st.JobHash == "" {
		t.Fatalf("job brief and hash should ride in the state frame: %+v", st)
	}
	if len(uploads) != 1 || uploads[0].Payload.Days[0].Attempts[0].Events != 65 {
		t.Fatalf("server-requested job snapshot should be uploaded once: %+v", uploads)
	}

	// Idle heartbeat: nothing about the job changes, so no state and no job upload.
	now = now.Add(61 * time.Second)
	if err := mgr.PushOnce(ctx, false); err != nil {
		t.Fatalf("heartbeat: %v", err)
	}
	if last := pushes[len(pushes)-1]; last.State != nil || len(uploads) != 1 {
		t.Fatalf("idle heartbeat must not resend job data: state=%v uploads=%d", last.State != nil, len(uploads))
	}

	// A new run result changes the hash: state goes out at once and the job is re-uploaded.
	provider.jmu.Lock()
	provider.snap = sampleJobDays("国内 OSS 上传失败")
	provider.jmu.Unlock()
	if err := mgr.PushOnce(ctx, false); err != nil {
		t.Fatalf("job change push: %v", err)
	}
	if len(uploads) != 2 || uploads[1].Payload.Days[0].Summary != "国内 OSS 上传失败" {
		t.Fatalf("changed job should be re-uploaded: %+v", uploads)
	}

	// The viewing device reads the peer's job on demand, once, and overlays live production state.
	if err := mgr.PullOnce(ctx); err != nil {
		t.Fatalf("pull: %v", err)
	}
	board, err := mgr.DeviceBoard("dev-home", "b")
	if err != nil || board.Board.Items[0].Job == nil || board.Board.Items[0].Job.Summary != "全部阶段成功" {
		t.Fatalf("synced board row should carry the job brief: %+v err=%v", board.Board.Items, err)
	}
	days, err := mgr.DeviceJobDays(ctx, "dev-home", "aics-daily", 30)
	if err != nil || len(days.Days) != 1 || len(days.Days[0].Attempts[0].Partitions) != 1 {
		t.Fatalf("DeviceJobDays: %+v err=%v", days, err)
	}
	detail, err := mgr.DeviceJobDay(ctx, "dev-home", "aics-daily", "2026-11-21")
	if err != nil || detail.Fetch == nil || len(detail.Production) != 1 || detail.Production[0].State != "down" {
		t.Fatalf("DeviceJobDay should overlay production state: %+v err=%v", detail, err)
	}
	if jobGets != 1 {
		t.Fatalf("job snapshot should be fetched once and cached, got %d fetches", jobGets)
	}
	if _, err := mgr.DeviceJobDays(ctx, "dev-home", "prod-api", 30); err == nil {
		t.Fatal("a non-job monitor must not serve job records")
	}
}

func TestTrimJobDaysForSyncDropsOldPartitionsFirst(t *testing.T) {
	snap := api.JobDays{Job: api.JobInfo{ID: "j"}}
	for d := 0; d < 30; d++ {
		parts := make([]jobs.Partition, 40)
		for i := range parts {
			parts[i] = jobs.Partition{Region: "cn", HourLabel: fmt.Sprintf("day %d hour %d", d, i), TargetDir: strings.Repeat("x", 300)}
		}
		snap.Days = append(snap.Days, jobs.Day{Date: fmt.Sprintf("d%02d", d), Status: jobs.Success,
			Attempts: []jobs.Attempt{{ID: "a", Stages: []jobs.StageView{{ID: "fetch"}}, Partitions: parts}}})
	}
	trimmed := TrimJobDaysForSync(snap, 64*1024)
	raw, _ := json.Marshal(trimmed)
	if len(raw) > 64*1024 {
		t.Fatalf("trimmed payload %d bytes over budget", len(raw))
	}
	if len(trimmed.Days) != 30 || len(trimmed.Days[0].Attempts[0].Partitions) != 40 {
		t.Fatalf("newest day and all 30 statuses must survive: days=%d", len(trimmed.Days))
	}
	if len(trimmed.Days[29].Attempts[0].Partitions) != 0 || len(trimmed.Days[29].Attempts[0].Stages) != 1 {
		t.Fatal("oldest day's partitions should go first while its stages stay")
	}
	if len(snap.Days[29].Attempts[0].Partitions) != 40 {
		t.Fatal("trimming must not mutate the caller's snapshot")
	}
}

// When the peer's state already names a new job hash but the service still holds the old record,
// the old record is shown for now and fetched again on the next read instead of being pinned.
func TestRemoteJobRecordIsRefetchedAfterAStaleReply(t *testing.T) {
	now := time.UnixMilli(1795350000000)
	clock := func() time.Time { return now }
	oldSnap, newSnap := sampleJobDays("全部阶段成功"), sampleJobDays("国内 OSS 上传失败")
	oldHash, newHash := HashJobDays(oldSnap), HashJobDays(newSnap)
	var (
		mu         sync.Mutex
		stateHash        = oldHash
		stored           = oldHash
		seq        int64 = 1
		jobGets    int
		lastIfNone string
	)
	client := &scriptedClient{handle: func(call scriptedCall) (int, any, string) {
		if status, body, etag, ok := signedInReply(call); ok {
			return status, body, etag
		}
		mu.Lock()
		defer mu.Unlock()
		switch {
		case strings.HasPrefix(call.Path, "/v1/sync/snapshot"):
			return 200, SyncSnapshotResponse{Seq: seq, Devices: []RemoteDevicePayload{{
				ID: "dev-home", Name: "家里电脑", Online: true, Seq: seq, MetaHash: "m", StateHash: stateHash,
				Meta: &CompactMeta{
					Boards:   []CompactBoard{{ID: "b", Name: "AICS", Monitors: []string{"aics-daily"}}},
					Monitors: []CompactMonitorMeta{{ID: "aics-daily", Name: "AICS 每日上传", Kind: "job"}},
				},
				State: &CompactState{TS: now.UnixMilli(), Monitors: map[string]CompactMonitorState{
					"aics-daily": {State: 1, Uptime: -1, Latency: -1, JobHash: stateHash},
				}},
			}}}, fmt.Sprintf(`"snap-%d"`, seq)
		case call.Path == "/v1/sync/devices/dev-home/jobs/aics-daily":
			jobGets++
			lastIfNone = call.IfNoneMatch
			if call.IfNoneMatch == `"`+stored+`"` {
				return 304, nil, call.IfNoneMatch
			}
			snap := oldSnap
			if stored == newHash {
				snap = newSnap
			}
			return 200, SyncJobResponse{DeviceID: "dev-home", MonitorID: "aics-daily", Hash: stored, Payload: snap}, `"` + stored + `"`
		}
		return 404, nil, ""
	}}
	mgr := newSignedIn(t, t.TempDir(), &fakeSecrets{}, client, clock, oneMonitor(10))
	ctx := context.Background()
	read := func() string {
		t.Helper()
		days, err := mgr.DeviceJobDays(ctx, "dev-home", "aics-daily", 30)
		if err != nil {
			t.Fatalf("DeviceJobDays: %v", err)
		}
		return days.Days[0].Summary
	}

	if err := mgr.PullOnce(ctx); err != nil {
		t.Fatalf("pull: %v", err)
	}
	if got := read(); got != "全部阶段成功" {
		t.Fatalf("first read = %q", got)
	}

	// The peer's state moves on before its new record reaches the service.
	mu.Lock()
	stateHash, seq = newHash, 2
	mu.Unlock()
	if err := mgr.PullOnce(ctx); err != nil {
		t.Fatalf("pull: %v", err)
	}
	if got := read(); got != "全部阶段成功" || lastIfNone != `"`+oldHash+`"` {
		t.Fatalf("stale read = %q, If-None-Match %q", got, lastIfNone)
	}

	// Once the upload lands, the next read gets it instead of the pinned old copy.
	mu.Lock()
	stored = newHash
	mu.Unlock()
	if got := read(); got != "国内 OSS 上传失败" {
		t.Fatalf("read after upload = %q (gets %d)", got, jobGets)
	}
	before := jobGets
	if got := read(); got != "国内 OSS 上传失败" || jobGets != before {
		t.Fatalf("matching hash should be served from cache: %q gets %d→%d", got, before, jobGets)
	}
}
