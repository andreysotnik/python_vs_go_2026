package mailer

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"io"
	"net"
	"net/smtp"
	"net/textproto"
	"time"

	"github.com/example/mailer/internal/config"
)

// Sender delivers a rendered message to the given envelope.
type Sender interface {
	Send(ctx context.Context, from string, rcpt []string, msg []byte) error
}

// SMTPSender delivers mail through an SMTP server using net/smtp.
type SMTPSender struct {
	Cfg       config.SMTP
	Timeout   time.Duration // per-attempt deadline for the whole SMTP session
	LocalName string        // EHLO name; "localhost" when empty
	TLSConfig *tls.Config   // optional override (tests)
}

// Send performs a single delivery attempt. Errors are classified: see IsPermanent.
func (s *SMTPSender) Send(ctx context.Context, from string, rcpt []string, msg []byte) (err error) {
	timeout := s.Timeout
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	tlsCfg := s.TLSConfig
	if tlsCfg == nil {
		tlsCfg = &tls.Config{ServerName: s.Cfg.Host, MinVersion: tls.VersionTLS12}
	}

	var conn net.Conn
	d := &net.Dialer{}
	if s.Cfg.TLS == config.TLSImplicit {
		conn, err = (&tls.Dialer{NetDialer: d, Config: tlsCfg}).DialContext(ctx, "tcp", s.Cfg.Addr())
	} else {
		conn, err = d.DialContext(ctx, "tcp", s.Cfg.Addr())
	}
	if err != nil {
		return classify(fmt.Errorf("connect %s: %w", s.Cfg.Addr(), err))
	}
	if dl, ok := ctx.Deadline(); ok {
		_ = conn.SetDeadline(dl)
	}
	c, err := smtp.NewClient(conn, s.Cfg.Host)
	if err != nil {
		_ = conn.Close()
		return classify(fmt.Errorf("smtp greeting: %w", err))
	}
	defer func() { _ = c.Close() }()

	if s.LocalName != "" {
		if err := c.Hello(s.LocalName); err != nil {
			return classify(fmt.Errorf("EHLO: %w", err))
		}
	}
	if s.Cfg.TLS == config.TLSStartTLS {
		if ok, _ := c.Extension("STARTTLS"); !ok {
			return Permanent(errors.New("server does not support STARTTLS (set SMTP_TLS=none to allow plaintext)"))
		}
		if err := c.StartTLS(tlsCfg); err != nil {
			return classify(fmt.Errorf("STARTTLS: %w", err))
		}
	}
	if s.Cfg.User != "" {
		if ok, _ := c.Extension("AUTH"); !ok {
			return Permanent(errors.New("server does not support AUTH but SMTP_USER is set"))
		}
		auth := smtp.PlainAuth("", s.Cfg.User, s.Cfg.Password, s.Cfg.Host)
		if err := c.Auth(auth); err != nil {
			return classify(fmt.Errorf("AUTH: %w", err))
		}
	}
	if err := c.Mail(from); err != nil {
		return classify(fmt.Errorf("MAIL FROM: %w", err))
	}
	for _, r := range rcpt {
		if err := c.Rcpt(r); err != nil {
			return classify(fmt.Errorf("RCPT TO <%s>: %w", r, err))
		}
	}
	w, err := c.Data()
	if err != nil {
		return classify(fmt.Errorf("DATA: %w", err))
	}
	if _, err := w.Write(msg); err != nil {
		_ = w.Close()
		return classify(fmt.Errorf("write body: %w", err))
	}
	if err := w.Close(); err != nil {
		return classify(fmt.Errorf("end of DATA: %w", err))
	}
	// The message is accepted at this point; a failing QUIT is not an error.
	_ = c.Quit()
	return nil
}

// PermanentError marks an error that must not be retried.
type PermanentError struct{ Err error }

func (e *PermanentError) Error() string { return e.Err.Error() }
func (e *PermanentError) Unwrap() error { return e.Err }

// Permanent wraps err as non-retryable.
func Permanent(err error) error {
	if err == nil {
		return nil
	}
	return &PermanentError{Err: err}
}

// IsPermanent reports whether err should not be retried.
func IsPermanent(err error) bool {
	var p *PermanentError
	return errors.As(err, &p)
}

// classify decides whether an SMTP/transport error is temporary or permanent:
//   - SMTP 5xx replies are permanent; 4xx replies are temporary;
//   - network errors (timeouts, refused/reset connections, EOF) are temporary;
//   - TLS certificate problems and anything else are permanent.
func classify(err error) error {
	var tp *textproto.Error
	if errors.As(err, &tp) {
		if tp.Code >= 500 {
			return Permanent(err)
		}
		return err
	}
	var certErr *tls.CertificateVerificationError
	var unknownAuth x509.UnknownAuthorityError
	var hostErr x509.HostnameError
	if errors.As(err, &certErr) || errors.As(err, &unknownAuth) || errors.As(err, &hostErr) {
		return Permanent(err)
	}
	var netErr net.Error
	if errors.As(err, &netErr) || errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) ||
		errors.Is(err, context.DeadlineExceeded) {
		return err
	}
	return Permanent(err)
}
