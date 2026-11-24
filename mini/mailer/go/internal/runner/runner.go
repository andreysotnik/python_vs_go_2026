// Package runner decides which jobs fire in a slot and sends them exactly once.
package runner

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"time"

	"github.com/example/mailer/internal/config"
	"github.com/example/mailer/internal/mailer"
	"github.com/example/mailer/internal/schedule"
	"github.com/example/mailer/internal/state"
	"github.com/example/mailer/internal/tmpl"
)

// Runner executes jobs for schedule slots.
type Runner struct {
	Jobs   []*config.Job
	From   string
	Store  *state.Store
	Sender mailer.Sender
	Retry  mailer.Retry
	Log    *slog.Logger
	// Now returns the current time (defaults to time.Now).
	Now func() time.Time
	// SleepUntil blocks until t or ctx is done (defaults to a timer).
	SleepUntil func(ctx context.Context, t time.Time) error
	// MaxMissedLog caps how many missed slots are logged per job (default 50).
	MaxMissedLog int
}

// Result summarises one slot.
type Result struct {
	Sent, Skipped, Failed int
}

// ErrJobsFailed is returned by RunSlot when at least one job failed.
var ErrJobsFailed = errors.New("some jobs failed")

func (r *Runner) now() time.Time {
	if r.Now != nil {
		return r.Now()
	}
	return time.Now()
}

// RunSlot sends every job that is due in the minute containing at.
//
// Cancelling ctx stops the runner from starting new jobs, but a job whose
// delivery has already begun is completed (including its retries) so that a
// shutdown never interrupts an email mid-flight.
func (r *Runner) RunSlot(ctx context.Context, at time.Time) (Result, error) {
	slot := schedule.Slot(at)
	var res Result
	for _, job := range r.Jobs {
		if !job.Sched.Matches(slot) {
			continue
		}
		log := r.Log.With("job", job.Name, "slot", slot.In(job.Location()).Format(time.RFC3339))
		if ctx.Err() != nil {
			log.Warn("shutdown requested, job not started")
			res.Failed++
			continue
		}
		sent, err := r.Store.IsSent(job.Name, slot)
		if err != nil {
			log.Error("state lookup failed", "error", err.Error())
			res.Failed++
			continue
		}
		if sent {
			log.Info("already sent for this slot, skipping")
			res.Skipped++
			continue
		}
		if err := r.sendJob(context.WithoutCancel(ctx), log, job, slot); err != nil {
			log.Error("job failed", "error", err.Error())
			res.Failed++
			continue
		}
		res.Sent++
	}
	if res.Failed > 0 {
		return res, ErrJobsFailed
	}
	return res, nil
}

func (r *Runner) sendJob(ctx context.Context, log *slog.Logger, job *config.Job, slot time.Time) error {
	msg, err := r.BuildMessage(job, slot)
	if err != nil {
		return err
	}
	raw, err := msg.Bytes()
	if err != nil {
		return fmt.Errorf("render message: %w", err)
	}
	from, err := msg.EnvelopeFrom()
	if err != nil {
		return err
	}
	rcpt, err := msg.Recipients()
	if err != nil {
		return err
	}
	start := r.now()
	log.Info("sending", "recipients", rcpt, "message_id", msg.MessageID, "size", len(raw))
	err = r.Retry.Do(ctx, log, func(ctx context.Context) error {
		return r.Sender.Send(ctx, from, rcpt, raw)
	})
	if err != nil {
		return err
	}
	if err := r.Store.MarkSent(state.Record{Job: job.Name, Slot: slot, SentAt: r.now().UTC(), MessageID: msg.MessageID}); err != nil {
		// The email is out; failing to persist risks a duplicate after restart.
		log.Error("sent but failed to persist state", "error", err.Error())
		return fmt.Errorf("persist state: %w", err)
	}
	log.Info("sent", "message_id", msg.MessageID, "duration_ms", r.now().Sub(start).Milliseconds())
	return nil
}

// BuildMessage renders the job's templates and loads attachments.
func (r *Runner) BuildMessage(job *config.Job, slot time.Time) (*mailer.Message, error) {
	loc := job.Location()
	now := r.now()
	msg := &mailer.Message{
		From:      r.From,
		To:        job.To,
		Cc:        job.Cc,
		Subject:   tmpl.Render(job.Subject, job.Name, slot, loc),
		Body:      tmpl.Render(job.Body, job.Name, slot, loc),
		HTML:      job.HTML,
		Date:      now.In(loc),
		MessageID: mailer.NewMessageID(r.From, now),
	}
	for _, p := range job.AttachmentPaths {
		data, err := os.ReadFile(p)
		if err != nil {
			return nil, mailer.Permanent(fmt.Errorf("read attachment: %w", err))
		}
		msg.Attachments = append(msg.Attachments, mailer.Attachment{Filename: p, Data: data})
	}
	return msg, nil
}

// Daemon fires due jobs at the start of every minute until ctx is cancelled.
// Slots missed while the process was down (or while it was blocked) are only
// logged, never sent retroactively.
func (r *Runner) Daemon(ctx context.Context) error {
	sleepUntil := r.SleepUntil
	if sleepUntil == nil {
		sleepUntil = sleepUntilTimer
	}
	last, err := r.Store.LastTick()
	if err != nil {
		r.Log.Warn("cannot read last tick, missed-run detection disabled for this start", "error", err.Error())
	}
	slot := schedule.Slot(r.now())
	if !last.IsZero() && last.Before(slot) {
		r.logMissed(last.Add(time.Minute), slot)
	}
	r.Log.Info("daemon started", "jobs", len(r.Jobs), "first_slot", slot.UTC().Format(time.RFC3339))
	for {
		res, err := r.RunSlot(ctx, slot)
		if res.Sent+res.Skipped+res.Failed > 0 {
			r.Log.Info("slot processed", "slot", slot.UTC().Format(time.RFC3339),
				"sent", res.Sent, "skipped", res.Skipped, "failed", res.Failed)
		}
		_ = err // failures are logged per job; the daemon keeps running
		if err := r.Store.SetLastTick(slot); err != nil {
			r.Log.Error("cannot persist last tick", "error", err.Error())
		}
		if ctx.Err() != nil {
			break
		}
		next := slot.Add(time.Minute)
		if err := sleepUntil(ctx, next); err != nil {
			break
		}
		cur := schedule.Slot(r.now())
		if cur.Before(next) {
			cur = next // clock went backwards or woke early: still process "next"
		}
		if cur.After(next) {
			r.logMissed(next, cur)
		}
		slot = cur
	}
	r.Log.Info("daemon stopped")
	return nil
}

// logMissed logs each job firing in the half-open interval [from, to).
func (r *Runner) logMissed(from, to time.Time) {
	limit := r.MaxMissedLog
	if limit <= 0 {
		limit = 50
	}
	for _, job := range r.Jobs {
		n := 0
		for t := job.Sched.Next(from.Add(-time.Second)); t.Before(to); t = job.Sched.Next(t) {
			n++
			if n <= limit {
				r.Log.Warn("missed run while not running, not sending", "job", job.Name,
					"slot", t.Format(time.RFC3339))
			}
			if n > 100000 {
				break
			}
		}
		if n > limit {
			r.Log.Warn("more missed runs not logged individually", "job", job.Name, "total_missed", n)
		}
	}
}

func sleepUntilTimer(ctx context.Context, t time.Time) error {
	timer := time.NewTimer(time.Until(t))
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
