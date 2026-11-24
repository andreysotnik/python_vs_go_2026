package mailer

import (
	"context"
	"errors"
	"io"
	"net"
	"net/textproto"
	"testing"
	"time"

	"github.com/example/mailer/internal/config"
	"github.com/example/mailer/internal/smtptest"
)

func newSender(srv *smtptest.Server) *SMTPSender {
	host, port := srv.Addr()
	return &SMTPSender{Cfg: config.SMTP{Host: host, Port: port, TLS: config.TLSNone, From: "a@b.c"}, Timeout: 5 * time.Second}
}

func noSleep(context.Context, time.Duration) error { return nil }

func TestSMTPSendOK(t *testing.T) {
	srv := smtptest.Start(t, nil)
	msg := []byte("Subject: x\r\n\r\n.leading dot\r\nbody\r\n")
	if err := newSender(srv).Send(context.Background(), "a@b.c", []string{"x@y.z", "w@y.z"}, msg); err != nil {
		t.Fatal(err)
	}
	got := srv.Messages()
	if len(got) != 1 || got[0].From != "a@b.c" || len(got[0].Rcpt) != 2 {
		t.Fatalf("got %+v", got)
	}
	if string(got[0].Data) != string(msg) {
		t.Fatalf("data %q", got[0].Data)
	}
}

func TestSMTPTemporaryErrorRetried(t *testing.T) {
	srv := smtptest.Start(t, func(conn int, verb string) *smtptest.Reply {
		if verb == "DATA_END" && conn < 3 {
			return &smtptest.Reply{Code: 451, Text: "local error, try again"}
		}
		return nil
	})
	s := newSender(srv)
	r := Retry{Attempts: 3, BaseDelay: time.Millisecond, Sleep: noSleep}
	err := r.Do(context.Background(), nil, func(ctx context.Context) error {
		return s.Send(ctx, "a@b.c", []string{"x@y.z"}, []byte("Subject: x\r\n\r\nhi\r\n"))
	})
	if err != nil {
		t.Fatal(err)
	}
	if srv.Connections() != 3 || len(srv.Messages()) != 1 {
		t.Fatalf("connections=%d messages=%d", srv.Connections(), len(srv.Messages()))
	}
}

func TestSMTPPermanentErrorNotRetried(t *testing.T) {
	srv := smtptest.Start(t, func(_ int, verb string) *smtptest.Reply {
		if verb == "RCPT" {
			return &smtptest.Reply{Code: 550, Text: "no such user"}
		}
		return nil
	})
	s := newSender(srv)
	r := Retry{Attempts: 3, BaseDelay: time.Millisecond, Sleep: noSleep}
	err := r.Do(context.Background(), nil, func(ctx context.Context) error {
		return s.Send(ctx, "a@b.c", []string{"x@y.z"}, []byte("x"))
	})
	if !IsPermanent(err) {
		t.Fatalf("expected permanent error, got %v", err)
	}
	if srv.Connections() != 1 {
		t.Fatalf("connections=%d, want 1", srv.Connections())
	}
}

func TestSMTPGreeting421IsTemporary(t *testing.T) {
	srv := smtptest.Start(t, func(_ int, verb string) *smtptest.Reply {
		if verb == "CONNECT" {
			return &smtptest.Reply{Code: 421, Text: "busy"}
		}
		return nil
	})
	err := newSender(srv).Send(context.Background(), "a@b.c", []string{"x@y.z"}, []byte("x"))
	if err == nil || IsPermanent(err) {
		t.Fatalf("expected temporary error, got %v", err)
	}
}

func TestConnectionRefusedIsTemporary(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	_ = ln.Close()
	s := &SMTPSender{Cfg: config.SMTP{Host: "127.0.0.1", Port: port, TLS: config.TLSNone}, Timeout: time.Second}
	err = s.Send(context.Background(), "a@b.c", []string{"x@y.z"}, []byte("x"))
	if err == nil || IsPermanent(err) {
		t.Fatalf("expected temporary error, got %v", err)
	}
}

func TestStartTLSRequiredButMissing(t *testing.T) {
	srv := smtptest.Start(t, nil)
	s := newSender(srv)
	s.Cfg.TLS = config.TLSStartTLS
	err := s.Send(context.Background(), "a@b.c", []string{"x@y.z"}, []byte("x"))
	if !IsPermanent(err) {
		t.Fatalf("expected permanent error, got %v", err)
	}
}

func TestClassify(t *testing.T) {
	cases := []struct {
		err  error
		perm bool
	}{
		{&textproto.Error{Code: 550, Msg: "x"}, true},
		{&textproto.Error{Code: 554, Msg: "x"}, true},
		{&textproto.Error{Code: 421, Msg: "x"}, false},
		{&textproto.Error{Code: 452, Msg: "x"}, false},
		{io.EOF, false},
		{context.DeadlineExceeded, false},
		{&net.OpError{Op: "dial", Err: errors.New("refused")}, false},
		{errors.New("unencrypted connection"), true},
	}
	for _, c := range cases {
		if got := IsPermanent(classify(c.err)); got != c.perm {
			t.Errorf("classify(%v) permanent=%v, want %v", c.err, got, c.perm)
		}
	}
}
