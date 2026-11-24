package runner

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/example/mailer/internal/config"
	"github.com/example/mailer/internal/mailer"
	"github.com/example/mailer/internal/state"
)

type sent struct {
	from string
	rcpt []string
	msg  []byte
}

type fakeSender struct {
	mu    sync.Mutex
	sent  []sent
	errs  []error // returned in order before succeeding
	block chan struct{}
	start chan struct{}
}

func (f *fakeSender) Send(_ context.Context, from string, rcpt []string, msg []byte) error {
	if f.start != nil {
		f.start <- struct{}{}
	}
	if f.block != nil {
		<-f.block
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.errs) > 0 {
		err := f.errs[0]
		f.errs = f.errs[1:]
		return err
	}
	f.sent = append(f.sent, sent{from, rcpt, msg})
	return nil
}

func (f *fakeSender) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.sent)
}

func mustJobs(t *testing.T, y string) []*config.Job {
	t.Helper()
	cfg, err := config.Parse([]byte(y), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return cfg.Jobs
}

const jobsYAML = `
jobs:
  - name: daily
    schedule: "0 9 * * 1-5"
    timezone: Europe/Moscow
    to: [team@example.com]
    cc: [boss@example.com]
    subject: "Отчёт {{job}} за {{date}}"
    body: "Дата {{datetime}}"
  - name: hourly
    schedule: "0 * * * *"
    to: [ops@example.com]
    subject: "hourly"
`

func newRunner(t *testing.T, sender mailer.Sender, y string) (*Runner, *bytes.Buffer) {
	t.Helper()
	store, err := state.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	var logs bytes.Buffer
	return &Runner{
		Jobs:   mustJobs(t, y),
		From:   "bot@example.com",
		Store:  store,
		Sender: sender,
		Retry:  mailer.Retry{Attempts: 3, BaseDelay: time.Millisecond, Sleep: func(context.Context, time.Duration) error { return nil }},
		Log:    slog.New(slog.NewJSONHandler(&logs, nil)),
	}, &logs
}

var slot0900 = time.Date(2026, 11, 19, 9, 0, 0, 0, time.FixedZone("MSK", 3*3600))

func TestRunSlotSendsDueJobsOnce(t *testing.T) {
	fs := &fakeSender{}
	r, logs := newRunner(t, fs, jobsYAML)

	res, err := r.RunSlot(context.Background(), slot0900.Add(25*time.Second))
	if err != nil || res.Sent != 2 {
		t.Fatalf("first run: %+v %v", res, err)
	}
	// Second run for the same slot must not send again (dedup).
	res, err = r.RunSlot(context.Background(), slot0900)
	if err != nil || res.Sent != 0 || res.Skipped != 2 {
		t.Fatalf("second run: %+v %v", res, err)
	}
	if fs.count() != 2 {
		t.Fatalf("sent %d messages, want 2", fs.count())
	}
	if !strings.Contains(logs.String(), "already sent") {
		t.Fatal("dedup not logged")
	}
	daily := fs.sent[0]
	if strings.Join(daily.rcpt, ",") != "team@example.com,boss@example.com" {
		t.Fatalf("rcpt %v", daily.rcpt)
	}
}

func TestRunSlotNothingDue(t *testing.T) {
	fs := &fakeSender{}
	r, _ := newRunner(t, fs, jobsYAML)
	res, err := r.RunSlot(context.Background(), slot0900.Add(time.Minute))
	if err != nil || res != (Result{}) || fs.count() != 0 {
		t.Fatalf("%+v %v", res, err)
	}
}

func TestFailedJobNotMarkedAndRetriedNextTime(t *testing.T) {
	fs := &fakeSender{errs: []error{mailer.Permanent(errors.New("550 rejected"))}}
	r, _ := newRunner(t, fs, `
jobs:
  - name: one
    schedule: "* * * * *"
    to: [a@example.com]
    subject: s
`)
	res, err := r.RunSlot(context.Background(), slot0900)
	if !errors.Is(err, ErrJobsFailed) || res.Failed != 1 {
		t.Fatalf("%+v %v", res, err)
	}
	if ok, _ := r.Store.IsSent("one", slot0900); ok {
		t.Fatal("failed job must not be marked as sent")
	}
	// A manual re-run of the same slot can still deliver it.
	res, err = r.RunSlot(context.Background(), slot0900)
	if err != nil || res.Sent != 1 {
		t.Fatalf("%+v %v", res, err)
	}
}

func TestTemporaryErrorsRetried(t *testing.T) {
	fs := &fakeSender{errs: []error{errors.New("421 busy"), errors.New("421 busy")}}
	r, _ := newRunner(t, fs, `
jobs:
  - name: one
    schedule: "* * * * *"
    to: [a@example.com]
    subject: s
`)
	if res, err := r.RunSlot(context.Background(), slot0900); err != nil || res.Sent != 1 {
		t.Fatalf("%+v %v", res, err)
	}
}

func TestBuildMessageTemplates(t *testing.T) {
	r, _ := newRunner(t, &fakeSender{}, jobsYAML)
	msg, err := r.BuildMessage(r.Jobs[0], slot0900.UTC())
	if err != nil {
		t.Fatal(err)
	}
	if msg.Subject != "Отчёт daily за 2026-11-19" || msg.Body != "Дата 2026-11-19 09:00 +03:00" {
		t.Fatalf("subject=%q body=%q", msg.Subject, msg.Body)
	}
	if msg.MessageID == "" {
		t.Fatal("no message id")
	}
}

func TestShutdownWaitsForCurrentEmail(t *testing.T) {
	fs := &fakeSender{block: make(chan struct{}), start: make(chan struct{}, 4)}
	r, logs := newRunner(t, fs, `
jobs:
  - name: first
    schedule: "* * * * *"
    to: [a@example.com]
    subject: s
  - name: second
    schedule: "* * * * *"
    to: [b@example.com]
    subject: s
`)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan Result)
	go func() {
		res, _ := r.RunSlot(ctx, slot0900)
		done <- res
	}()
	<-fs.start // first email is being sent
	cancel()   // SIGTERM arrives mid-send
	time.Sleep(20 * time.Millisecond)
	select {
	case <-done:
		t.Fatal("RunSlot returned before the in-flight email finished")
	default:
	}
	close(fs.block)
	res := <-done
	if res.Sent != 1 || res.Failed != 1 || fs.count() != 1 {
		t.Fatalf("res=%+v sent=%d", res, fs.count())
	}
	if ok, _ := r.Store.IsSent("first", slot0900); !ok {
		t.Fatal("in-flight email must be recorded")
	}
	if !strings.Contains(logs.String(), "shutdown requested") {
		t.Fatal("skipped job not logged")
	}
}

func TestDaemonTicksAndLogsMissedRuns(t *testing.T) {
	fs := &fakeSender{}
	r, logs := newRunner(t, fs, `
jobs:
  - name: every
    schedule: "* * * * *"
    to: [a@example.com]
    subject: s
`)
	start := time.Date(2026, 11, 19, 10, 0, 30, 0, time.UTC)
	// Process was down: last processed minute was 09:57.
	if err := r.Store.SetLastTick(start.Add(-3*time.Minute - 30*time.Second)); err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	now := start
	r.Now = func() time.Time { mu.Lock(); defer mu.Unlock(); return now }
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	sleeps := 0
	r.SleepUntil = func(_ context.Context, t time.Time) error {
		mu.Lock()
		defer mu.Unlock()
		sleeps++
		switch sleeps {
		case 1:
			now = t.Add(time.Second) // 10:01:01
		case 2:
			now = t.Add(3 * time.Minute) // stalled: woke at 10:05, 10:02-10:04 missed
		default:
			cancel()
			return context.Canceled
		}
		return nil
	}
	if err := r.Daemon(ctx); err != nil {
		t.Fatal(err)
	}
	// Sent: 10:00 (current minute at start), 10:01, 10:05.
	if fs.count() != 3 {
		t.Fatalf("sent %d, want 3", fs.count())
	}
	var missed []string
	for _, line := range strings.Split(strings.TrimSpace(logs.String()), "\n") {
		var e map[string]any
		if err := json.Unmarshal([]byte(line), &e); err != nil {
			t.Fatalf("log line is not JSON: %q", line)
		}
		if strings.HasPrefix(e["msg"].(string), "missed run") {
			missed = append(missed, e["slot"].(string))
		}
	}
	want := "2026-11-19T09:58:00Z,2026-11-19T09:59:00Z,2026-11-19T10:02:00Z,2026-11-19T10:03:00Z,2026-11-19T10:04:00Z"
	if strings.Join(missed, ",") != want {
		t.Fatalf("missed = %v\nwant %v", missed, want)
	}
	if lt, _ := r.Store.LastTick(); !lt.Equal(time.Date(2026, 11, 19, 10, 5, 0, 0, time.UTC)) {
		t.Fatalf("last tick %v", lt)
	}
}

func TestDaemonRestartDoesNotResend(t *testing.T) {
	fs := &fakeSender{}
	r, _ := newRunner(t, fs, `
jobs:
  - name: every
    schedule: "* * * * *"
    to: [a@example.com]
    subject: s
`)
	r.Now = func() time.Time { return slot0900.Add(10 * time.Second) }
	r.SleepUntil = func(context.Context, time.Time) error { return context.Canceled }
	for i := 0; i < 2; i++ { // two "process starts" within the same minute
		if err := r.Daemon(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	if fs.count() != 1 {
		t.Fatalf("sent %d, want 1", fs.count())
	}
}

func TestLogMissedCap(t *testing.T) {
	r, logs := newRunner(t, &fakeSender{}, `
jobs:
  - name: every
    schedule: "* * * * *"
    to: [a@example.com]
    subject: s
`)
	r.MaxMissedLog = 2
	r.logMissed(slot0900, slot0900.Add(10*time.Minute))
	if c := strings.Count(logs.String(), "missed run while"); c != 2 {
		t.Fatalf("logged %d", c)
	}
	if !strings.Contains(logs.String(), `"total_missed":10`) {
		t.Fatalf("summary missing: %s", logs.String())
	}
}
