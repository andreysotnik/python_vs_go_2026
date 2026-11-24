package mailer

import (
	"context"
	"errors"
	"testing"
	"time"
)

type fakeSleep struct{ delays []time.Duration }

func (f *fakeSleep) sleep(_ context.Context, d time.Duration) error {
	f.delays = append(f.delays, d)
	return nil
}

func TestRetryTemporaryThenSuccess(t *testing.T) {
	fs := &fakeSleep{}
	r := Retry{Attempts: 3, BaseDelay: time.Second, Sleep: fs.sleep}
	calls := 0
	err := r.Do(context.Background(), nil, func(context.Context) error {
		calls++
		if calls < 3 {
			return errors.New("421 try later")
		}
		return nil
	})
	if err != nil || calls != 3 {
		t.Fatalf("err=%v calls=%d", err, calls)
	}
	if len(fs.delays) != 2 || fs.delays[0] != time.Second || fs.delays[1] != 2*time.Second {
		t.Fatalf("delays = %v, want [1s 2s]", fs.delays)
	}
}

func TestRetryGivesUpAfterAttempts(t *testing.T) {
	fs := &fakeSleep{}
	r := Retry{Attempts: 3, BaseDelay: 10 * time.Millisecond, Sleep: fs.sleep}
	calls := 0
	tmp := errors.New("temporary")
	err := r.Do(context.Background(), nil, func(context.Context) error { calls++; return tmp })
	if calls != 3 || !errors.Is(err, tmp) {
		t.Fatalf("calls=%d err=%v", calls, err)
	}
	if len(fs.delays) != 2 {
		t.Fatalf("delays %v", fs.delays)
	}
}

func TestRetryPermanentNotRetried(t *testing.T) {
	fs := &fakeSleep{}
	r := Retry{Attempts: 3, BaseDelay: time.Second, Sleep: fs.sleep}
	calls := 0
	err := r.Do(context.Background(), nil, func(context.Context) error {
		calls++
		return Permanent(errors.New("550 no such user"))
	})
	if calls != 1 || !IsPermanent(err) || len(fs.delays) != 0 {
		t.Fatalf("calls=%d err=%v delays=%v", calls, err, fs.delays)
	}
}

func TestRetryAbortedByContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	r := Retry{Attempts: 3, BaseDelay: time.Hour}
	calls := 0
	err := r.Do(ctx, nil, func(context.Context) error {
		calls++
		cancel()
		return errors.New("temporary")
	})
	if calls != 1 || !errors.Is(err, context.Canceled) {
		t.Fatalf("calls=%d err=%v", calls, err)
	}
}

func TestDelayCap(t *testing.T) {
	r := Retry{BaseDelay: time.Second, MaxDelay: 3 * time.Second}
	got := []time.Duration{r.Delay(1), r.Delay(2), r.Delay(3), r.Delay(10)}
	want := []time.Duration{time.Second, 2 * time.Second, 3 * time.Second, 3 * time.Second}
	for i := range got {
		if got[i] != want[i] {
			t.Fatalf("Delay = %v, want %v", got, want)
		}
	}
	if d := DefaultRetry(); d.Attempts != 3 {
		t.Fatalf("default attempts %d", d.Attempts)
	}
}
