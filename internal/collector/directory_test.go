package collector

import (
	"context"
	"errors"
	"testing"
	"time"
)

type fakeWorkingDirectoryReader struct {
	calls int
	value string
	err   error
}

func (f *fakeWorkingDirectoryReader) ReadDirectory(context.Context, ProcessIdentity) (string, error) {
	f.calls++
	return f.value, f.err
}

func TestWorkingDirectoryCacheFollowsIdentityAndTenSecondPeriod(t *testing.T) {
	ctx := context.Background()
	now := time.Unix(100, 0)
	process := sampleProcess()
	backend := &fakeProcesses{entries: []processEntry{{pid: 42, reader: process}}}
	source := newProcessSource(backend, func() time.Time { return now }, nil)
	reader := &fakeWorkingDirectoryReader{value: `D:\site\a`}
	source.directories = reader
	rows, err := source.Snapshot(ctx)
	if err != nil || len(rows) != 1 || reader.calls != 0 {
		t.Fatalf("snapshot eagerly read working directories: %+v %v", rows, err)
	}
	identity := rows[0].Identity()
	value, err := source.WorkingDirectory(ctx, identity)
	if err != nil || value != reader.value || reader.calls != 1 {
		t.Fatalf("first directory read failed: %q %v calls=%d", value, err, reader.calls)
	}
	reader.value = `D:\site\b`
	now = now.Add(9 * time.Second)
	value, _ = source.WorkingDirectory(ctx, identity)
	if value != `D:\site\a` || reader.calls != 1 {
		t.Fatal("directory re-read before ten seconds")
	}
	now = now.Add(time.Second)
	value, err = source.WorkingDirectory(ctx, identity)
	if value != reader.value || err != nil || reader.calls != 2 {
		t.Fatalf("directory was not refreshed: %q %v calls=%d", value, err, reader.calls)
	}
	process.createdAt++
	rows, err = source.Snapshot(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := source.WorkingDirectory(ctx, identity); err == nil || reader.calls != 2 {
		t.Fatal("reused PID inherited an old identity")
	}
	if _, err := source.WorkingDirectory(ctx, rows[0].Identity()); err != nil || reader.calls != 3 {
		t.Fatal("new identity reused stale directory")
	}
	backend.entries = nil
	if _, err := source.Snapshot(ctx); err != nil || len(source.workingDirs) != 0 {
		t.Fatal("exited identities retained working-directory cache")
	}
}

func TestWorkingDirectoryEmptyAndErrorsRemainUnknownWithoutRepeatedReads(t *testing.T) {
	for _, failure := range []error{nil, errors.New("fake denied")} {
		now := time.Unix(100, 0)
		source := newProcessSource(&fakeProcesses{entries: []processEntry{{pid: 42, reader: sampleProcess()}}}, func() time.Time { return now }, nil)
		reader := &fakeWorkingDirectoryReader{err: failure}
		source.directories = reader
		rows, err := source.Snapshot(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		for i := 0; i < 3; i++ {
			if value, err := source.WorkingDirectory(context.Background(), rows[0].Identity()); value != "" || err == nil {
				t.Fatalf("empty/denied directory accepted: %q %v", value, err)
			}
		}
		if reader.calls != 1 {
			t.Fatal("failed directory read was repeated within cache period")
		}
		reader.value, reader.err = `D:\site`, nil
		now = now.Add(10 * time.Second)
		if _, err := source.WorkingDirectory(context.Background(), rows[0].Identity()); err != nil || reader.calls != 2 {
			t.Fatal("failed directory cache did not recover")
		}
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		if _, err := source.WorkingDirectory(ctx, rows[0].Identity()); !errors.Is(err, context.Canceled) || reader.calls != 2 {
			t.Fatal("canceled directory read touched backend")
		}
	}
}
