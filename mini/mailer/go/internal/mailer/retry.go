package mailer

import (
	"context"
	"fmt"
	"log/slog"
	"time"
)

// Retry configures exponential backoff for temporary errors.
type Retry struct {
	Attempts  int           // total attempts, including the first one
	BaseDelay time.Duration // delay before the 2nd attempt; doubled each time
	MaxDelay  time.Duration // cap for a single delay (0 = no cap)
	// Sleep waits for d or until ctx is done. Defaults to a timer-based sleep.
	Sleep func(ctx context.Context, d time.Duration) error
}

// DefaultRetry is 3 attempts with 1s, 2s delays.
func DefaultRetry() Retry {
	return Retry{Attempts: 3, BaseDelay: time.Second, MaxDelay: 30 * time.Second}
}

// Delay returns the wait before attempt n+1 (n starts at 1).
func (r Retry) Delay(n int) time.Duration {
	d := r.BaseDelay
	for i := 1; i < n; i++ {
		d *= 2
		if r.MaxDelay > 0 && d >= r.MaxDelay {
			return r.MaxDelay
		}
	}
	return d
}

// Do calls fn until it succeeds, returns a permanent error, or attempts run out.
func (r Retry) Do(ctx context.Context, log *slog.Logger, fn func(ctx context.Context) error) error {
	attempts := r.Attempts
	if attempts < 1 {
		attempts = 1
	}
	sleep := r.Sleep
	if sleep == nil {
		sleep = sleepCtx
	}
	var err error
	for n := 1; n <= attempts; n++ {
		err = fn(ctx)
		if err == nil {
			return nil
		}
		if IsPermanent(err) {
			if log != nil {
				log.Error("permanent error, not retrying", "attempt", n, "error", err.Error())
			}
			return err
		}
		if n == attempts {
			break
		}
		d := r.Delay(n)
		if log != nil {
			log.Warn("temporary error, will retry", "attempt", n, "max_attempts", attempts,
				"retry_in", d.String(), "error", err.Error())
		}
		if serr := sleep(ctx, d); serr != nil {
			return fmt.Errorf("retry aborted: %w (last error: %w)", serr, err)
		}
	}
	return fmt.Errorf("giving up after %d attempts: %w", attempts, err)
}

func sleepCtx(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}
