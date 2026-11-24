// Command mailer sends scheduled email newsletters.
//
//	mailer validate --config jobs.yaml
//	mailer once     --config jobs.yaml [--at 2026-11-19T09:00:00+03:00]
//	mailer run      --config jobs.yaml
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"
	_ "time/tzdata" // embed the IANA database so minimal images work

	"github.com/example/mailer/internal/config"
	"github.com/example/mailer/internal/mailer"
	"github.com/example/mailer/internal/runner"
	"github.com/example/mailer/internal/state"
)

// Exit codes.
const (
	exitOK      = 0
	exitFailed  = 1 // at least one job failed to send
	exitUsage   = 2 // bad CLI usage, invalid config or environment
	usageString = `Usage:
  mailer validate --config jobs.yaml
  mailer once     --config jobs.yaml [--at RFC3339]
  mailer run      --config jobs.yaml

Environment: SMTP_HOST, SMTP_PORT, SMTP_USER, SMTP_PASSWORD, SMTP_FROM,
             SMTP_TLS=none|starttls|tls, STATE_DIR
`
)

func main() {
	os.Exit(run(os.Args[1:], os.Getenv, os.Stdout, os.Stderr))
}

func run(args []string, getenv func(string) string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprint(stderr, usageString)
		return exitUsage
	}
	cmd, rest := args[0], args[1:]
	fs := flag.NewFlagSet(cmd, flag.ContinueOnError)
	fs.SetOutput(stderr)
	cfgPath := fs.String("config", "jobs.yaml", "path to jobs.yaml")
	var at *string
	if cmd == "once" {
		at = fs.String("at", "", "slot time in RFC3339 (default: now)")
	}
	switch cmd {
	case "validate", "once", "run":
	case "-h", "--help", "help":
		fmt.Fprint(stdout, usageString)
		return exitOK
	default:
		fmt.Fprintf(stderr, "unknown command %q\n\n%s", cmd, usageString)
		return exitUsage
	}
	if err := fs.Parse(rest); err != nil {
		return exitUsage
	}
	if fs.NArg() > 0 {
		fmt.Fprintf(stderr, "unexpected arguments: %v\n", fs.Args())
		return exitUsage
	}

	log := slog.New(slog.NewJSONHandler(stdout, &slog.HandlerOptions{Level: logLevel(getenv("LOG_LEVEL"))}))

	cfg, err := config.Load(*cfgPath)
	if cmd == "validate" {
		if err != nil {
			fmt.Fprintf(stderr, "%s: INVALID\n%v\n", *cfgPath, err)
			return exitUsage
		}
		fmt.Fprintf(stdout, "%s: OK (%d jobs)\n", *cfgPath, len(cfg.Jobs))
		for _, j := range cfg.Jobs {
			fmt.Fprintf(stdout, "  - %s: %q %s, next run %s\n", j.Name, j.Schedule, j.Location(),
				j.Sched.Next(time.Now()).Format(time.RFC3339))
		}
		return exitOK
	}
	if err != nil {
		log.Error("invalid config", "config", *cfgPath, "error", err.Error())
		return exitUsage
	}

	smtpCfg, err := config.SMTPFromEnv(getenv)
	if err != nil {
		log.Error("invalid SMTP environment", "error", err.Error())
		return exitUsage
	}
	store, err := state.Open(getenv("STATE_DIR"))
	if err != nil {
		log.Error("cannot open state", "error", err.Error())
		return exitUsage
	}
	r := &runner.Runner{
		Jobs:   cfg.Jobs,
		From:   smtpCfg.From,
		Store:  store,
		Sender: &mailer.SMTPSender{Cfg: smtpCfg, Timeout: 30 * time.Second},
		Retry:  mailer.DefaultRetry(),
		Log:    log,
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	sigCh := make(chan os.Signal, 2)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	defer signal.Stop(sigCh)
	done := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		select {
		case sig := <-sigCh:
			log.Info("shutdown signal received, finishing current email", "signal", sig.String())
			cancel()
		case <-done:
			return
		}
		select {
		case sig := <-sigCh:
			log.Warn("second signal received, exiting immediately", "signal", sig.String())
			os.Exit(130)
		case <-done:
		}
	}()
	defer wg.Wait()
	defer close(done)

	switch cmd {
	case "once":
		slot := time.Now()
		if *at != "" {
			slot, err = time.Parse(time.RFC3339, *at)
			if err != nil {
				log.Error("invalid --at, expected RFC3339 like 2026-11-19T09:00:00+03:00", "at", *at, "error", err.Error())
				return exitUsage
			}
		}
		res, err := r.RunSlot(ctx, slot)
		log.Info("once finished", "slot", slot.Truncate(time.Minute).Format(time.RFC3339),
			"sent", res.Sent, "skipped", res.Skipped, "failed", res.Failed)
		if errors.Is(err, runner.ErrJobsFailed) {
			return exitFailed
		}
		return exitOK
	default: // run
		if err := r.Daemon(ctx); err != nil {
			log.Error("daemon error", "error", err.Error())
			return exitFailed
		}
		return exitOK
	}
}

func logLevel(s string) slog.Level {
	var l slog.Level
	if err := l.UnmarshalText([]byte(s)); err != nil {
		return slog.LevelInfo
	}
	return l
}
