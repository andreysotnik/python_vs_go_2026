package config

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const validYAML = `
jobs:
  - name: daily-report
    schedule: "0 9 * * 1-5"
    timezone: "Europe/Moscow"
    to: ["team@example.com", "Boss <boss@example.com>"]
    cc: []
    subject: "Ежедневный отчёт за {{date}}"
    body: |
      Привет! Отчёт за {{date}} во вложении.
    html: false
    attachments: ["./files/report.pdf"]
`

func TestLoadValid(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "files"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "files", "report.pdf"), []byte("%PDF"), 0o644); err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(dir, "jobs.yaml")
	if err := os.WriteFile(p, []byte(validYAML), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(p)
	if err != nil {
		t.Fatal(err)
	}
	j := cfg.Jobs[0]
	if j.Sched == nil || j.Location().String() != "Europe/Moscow" {
		t.Fatalf("schedule not resolved: %+v", j)
	}
	if want := filepath.Join(dir, "files", "report.pdf"); j.AttachmentPaths[0] != want {
		t.Fatalf("attachment path = %q, want %q", j.AttachmentPaths[0], want)
	}
}

func TestValidationCollectsAllProblems(t *testing.T) {
	y := `
jobs:
  - name: bad
    schedule: "0 25 * * *"
    timezone: "Nowhere/City"
    to: ["not-an-email", "ok@example.com"]
    cc: ["also bad"]
    subject: ""
    attachments: ["missing.pdf"]
  - name: bad
    schedule: "* * * * *"
    to: []
    subject: "s"
  - schedule: "* * * * *"
    to: ["a@b.c"]
    subject: "s"
`
	_, err := Parse([]byte(y), t.TempDir())
	var ve *ValidationError
	if !errors.As(err, &ve) {
		t.Fatalf("expected ValidationError, got %v", err)
	}
	msg := err.Error()
	for _, want := range []string{
		"invalid cron expression", "unknown timezone", `"not-an-email"`, `"also bad"`,
		"subject is required", "missing.pdf", "duplicate job name",
		"at least one recipient", "name is required",
	} {
		if !strings.Contains(msg, want) {
			t.Errorf("error does not mention %q:\n%s", want, msg)
		}
	}
}

func TestUnknownFieldAndSyntax(t *testing.T) {
	if _, err := Parse([]byte("jobs:\n  - name: x\n    shedule: '* * * * *'\n"), "."); err == nil ||
		!strings.Contains(err.Error(), "shedule") {
		t.Fatalf("expected unknown field error, got %v", err)
	}
	if _, err := Parse([]byte("jobs: [\n"), "."); err == nil {
		t.Fatal("expected syntax error")
	}
	if _, err := Parse([]byte(""), "."); err == nil {
		t.Fatal("expected error for empty config")
	}
}

func env(m map[string]string) func(string) string { return func(k string) string { return m[k] } }

func TestSMTPFromEnv(t *testing.T) {
	s, err := SMTPFromEnv(env(map[string]string{
		"SMTP_HOST": "mail", "SMTP_PORT": "1025", "SMTP_FROM": "Bot <bot@example.com>", "SMTP_TLS": "none",
	}))
	if err != nil {
		t.Fatal(err)
	}
	if s.Addr() != "mail:1025" || s.TLS != TLSNone {
		t.Fatalf("got %+v", s)
	}
	s, err = SMTPFromEnv(env(map[string]string{"SMTP_HOST": "mail", "SMTP_FROM": "a@b.c", "SMTP_TLS": "tls"}))
	if err != nil || s.Port != 465 {
		t.Fatalf("default tls port: %+v %v", s, err)
	}
	s, err = SMTPFromEnv(env(map[string]string{"SMTP_HOST": "mail", "SMTP_FROM": "a@b.c"}))
	if err != nil || s.Port != 587 || s.TLS != TLSStartTLS {
		t.Fatalf("defaults: %+v %v", s, err)
	}
	_, err = SMTPFromEnv(env(map[string]string{"SMTP_PORT": "abc", "SMTP_TLS": "ssl", "SMTP_FROM": "bad", "SMTP_PASSWORD": "x"}))
	if err == nil {
		t.Fatal("expected error")
	}
	for _, want := range []string{"SMTP_HOST", "SMTP_PORT", "SMTP_TLS", "SMTP_FROM", "SMTP_USER"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error does not mention %s: %v", want, err)
		}
	}
}
