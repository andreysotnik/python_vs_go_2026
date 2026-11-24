package main

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"net/mail"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/example/mailer/internal/smtptest"
)

type env map[string]string

func (e env) get(k string) string { return e[k] }

func writeProject(t *testing.T) (dir string, attachment []byte) {
	t.Helper()
	dir = t.TempDir()
	attachment = append([]byte("%PDF-1.4\n"), bytes.Repeat([]byte{0xde, 0xad, 0xbe, 0xef}, 300)...)
	if err := os.MkdirAll(filepath.Join(dir, "files"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "files", "report.pdf"), attachment, 0o644); err != nil {
		t.Fatal(err)
	}
	cfg := `
jobs:
  - name: daily-report
    schedule: "0 9 * * 1-5"
    timezone: "Europe/Moscow"
    to: ["team@example.com", "boss@example.com"]
    cc: ["cc@example.com"]
    subject: "Ежедневный отчёт за {{date}}"
    body: |
      Привет! Отчёт за {{date}} во вложении.
    html: false
    attachments: ["./files/report.pdf"]
  - name: weekend
    schedule: "0 9 * * 0,6"
    timezone: "Europe/Moscow"
    to: ["weekend@example.com"]
    subject: "never on Thursday"
`
	if err := os.WriteFile(filepath.Join(dir, "jobs.yaml"), []byte(cfg), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir, attachment
}

func TestIntegrationOnceWithFakeSMTP(t *testing.T) {
	srv := smtptest.Start(t, nil)
	host, port := srv.Addr()
	dir, attachment := writeProject(t)
	e := env{
		"SMTP_HOST": host, "SMTP_PORT": fmt.Sprint(port), "SMTP_TLS": "none",
		"SMTP_FROM": "Рассылка <mailer@example.com>", "STATE_DIR": filepath.Join(dir, "state"),
	}
	args := []string{"once", "--config", filepath.Join(dir, "jobs.yaml"), "--at", "2026-11-19T09:00:00+03:00"}

	var out, errOut bytes.Buffer
	if code := run(args, e.get, &out, &errOut); code != 0 {
		t.Fatalf("exit %d\nstdout:%s\nstderr:%s", code, out.String(), errOut.String())
	}
	for _, line := range strings.Split(strings.TrimSpace(out.String()), "\n") {
		if !json.Valid([]byte(line)) {
			t.Fatalf("stdout line is not JSON: %q", line)
		}
	}

	msgs := srv.Messages()
	if len(msgs) != 1 {
		t.Fatalf("got %d messages, want 1", len(msgs))
	}
	got := msgs[0]
	if got.From != "mailer@example.com" {
		t.Fatalf("envelope from %q", got.From)
	}
	if strings.Join(got.Rcpt, ",") != "team@example.com,boss@example.com,cc@example.com" {
		t.Fatalf("envelope rcpt %v", got.Rcpt)
	}

	m, err := mail.ReadMessage(bytes.NewReader(got.Data))
	if err != nil {
		t.Fatal(err)
	}
	rawSubj := m.Header.Get("Subject")
	if !strings.HasPrefix(rawSubj, "=?UTF-8?") {
		t.Fatalf("subject not RFC 2047 encoded: %q", rawSubj)
	}
	subj, err := new(mime.WordDecoder).DecodeHeader(rawSubj)
	if err != nil || subj != "Ежедневный отчёт за 2026-11-19" {
		t.Fatalf("subject %q, %v", subj, err)
	}
	to, err := m.Header.AddressList("To")
	if err != nil || len(to) != 2 || to[0].Address != "team@example.com" || to[1].Address != "boss@example.com" {
		t.Fatalf("To %v %v", to, err)
	}
	if cc, _ := m.Header.AddressList("Cc"); len(cc) != 1 || cc[0].Address != "cc@example.com" {
		t.Fatalf("Cc %v", cc)
	}
	if m.Header.Get("Message-Id") == "" || m.Header.Get("Date") == "" {
		t.Fatal("Message-ID/Date missing")
	}

	_, params, err := mime.ParseMediaType(m.Header.Get("Content-Type"))
	if err != nil {
		t.Fatal(err)
	}
	mr := multipart.NewReader(m.Body, params["boundary"])
	textPart, err := mr.NextPart()
	if err != nil {
		t.Fatal(err)
	}
	text, _ := io.ReadAll(textPart)
	if !strings.Contains(string(text), "Привет! Отчёт за 2026-11-19 во вложении.") {
		t.Fatalf("body %q", text)
	}
	att, err := mr.NextPart()
	if err != nil {
		t.Fatal(err)
	}
	if att.FileName() != "report.pdf" {
		t.Fatalf("attachment name %q", att.FileName())
	}
	data, err := io.ReadAll(base64.NewDecoder(base64.StdEncoding, att))
	if err != nil || !bytes.Equal(data, attachment) {
		t.Fatalf("attachment content mismatch (%d bytes, err %v)", len(data), err)
	}

	// Re-running the same slot must not send a duplicate.
	out.Reset()
	if code := run(args, e.get, &out, &errOut); code != 0 {
		t.Fatalf("second run exit %d: %s", code, errOut.String())
	}
	if n := len(srv.Messages()); n != 1 {
		t.Fatalf("duplicate sent: %d messages", n)
	}
	if !strings.Contains(out.String(), "already sent") {
		t.Fatalf("expected dedup log, got %s", out.String())
	}
}

func TestOnceFailureExitCode(t *testing.T) {
	srv := smtptest.Start(t, func(_ int, verb string) *smtptest.Reply {
		if verb == "MAIL" {
			return &smtptest.Reply{Code: 554, Text: "rejected"}
		}
		return nil
	})
	host, port := srv.Addr()
	dir, _ := writeProject(t)
	e := env{"SMTP_HOST": host, "SMTP_PORT": fmt.Sprint(port), "SMTP_TLS": "none",
		"SMTP_FROM": "mailer@example.com", "STATE_DIR": filepath.Join(dir, "state")}
	var out, errOut bytes.Buffer
	code := run([]string{"once", "--config", filepath.Join(dir, "jobs.yaml"), "--at", "2026-11-19T06:00:00Z"}, e.get, &out, &errOut)
	if code != exitFailed {
		t.Fatalf("exit %d, want %d", code, exitFailed)
	}
	if srv.Connections() != 1 {
		t.Fatalf("permanent error retried: %d connections", srv.Connections())
	}
}

func TestValidateCommand(t *testing.T) {
	dir, _ := writeProject(t)
	var out, errOut bytes.Buffer
	if code := run([]string{"validate", "--config", filepath.Join(dir, "jobs.yaml")}, env{}.get, &out, &errOut); code != 0 {
		t.Fatalf("exit %d: %s", code, errOut.String())
	}
	if !strings.Contains(out.String(), "OK (2 jobs)") {
		t.Fatalf("stdout %q", out.String())
	}

	bad := filepath.Join(dir, "bad.yaml")
	_ = os.WriteFile(bad, []byte("jobs:\n  - name: x\n    schedule: '0 9 * *'\n    timezone: Mars/Base\n    to: [nope]\n    subject: s\n    attachments: [gone.pdf]\n"), 0o644)
	out.Reset()
	errOut.Reset()
	if code := run([]string{"validate", "--config", bad}, env{}.get, &out, &errOut); code != exitUsage {
		t.Fatalf("exit %d, want %d", code, exitUsage)
	}
	for _, want := range []string{"INVALID", "cron", "Mars/Base", "nope", "gone.pdf"} {
		if !strings.Contains(errOut.String(), want) {
			t.Errorf("stderr does not mention %q:\n%s", want, errOut.String())
		}
	}
}

func TestUsageErrors(t *testing.T) {
	var out, errOut bytes.Buffer
	if code := run(nil, env{}.get, &out, &errOut); code != exitUsage {
		t.Fatalf("no args: exit %d", code)
	}
	if code := run([]string{"bogus"}, env{}.get, &out, &errOut); code != exitUsage {
		t.Fatalf("bogus: exit %d", code)
	}
	dir, _ := writeProject(t)
	cfg := filepath.Join(dir, "jobs.yaml")
	if code := run([]string{"once", "--config", cfg}, env{}.get, &out, &errOut); code != exitUsage {
		t.Fatalf("missing SMTP env: exit %d", code)
	}
	e := env{"SMTP_HOST": "h", "SMTP_FROM": "a@b.c", "STATE_DIR": t.TempDir()}
	if code := run([]string{"once", "--config", cfg, "--at", "yesterday"}, e.get, &out, &errOut); code != exitUsage {
		t.Fatalf("bad --at: exit %d", code)
	}
}
