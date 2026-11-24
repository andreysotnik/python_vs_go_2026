// Package mailer builds MIME messages and delivers them over SMTP with retries.
package mailer

import (
	"bytes"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"mime"
	"mime/multipart"
	"mime/quotedprintable"
	"net/mail"
	"net/textproto"
	"path/filepath"
	"strings"
	"time"
)

// Attachment is a file attached to a message.
type Attachment struct {
	Filename    string
	ContentType string // optional; detected from the extension when empty
	Data        []byte
}

// Message is an email to be rendered as RFC 5322 / MIME.
type Message struct {
	From        string
	To          []string
	Cc          []string
	Subject     string
	Body        string
	HTML        bool
	Attachments []Attachment
	Date        time.Time
	MessageID   string // with or without angle brackets; generated when empty
}

// Recipients returns all envelope recipients (To + Cc), de-duplicated, as bare addresses.
func (m *Message) Recipients() ([]string, error) {
	seen := map[string]bool{}
	var out []string
	for _, raw := range append(append([]string{}, m.To...), m.Cc...) {
		a, err := mail.ParseAddress(raw)
		if err != nil {
			return nil, fmt.Errorf("recipient %q: %w", raw, err)
		}
		key := strings.ToLower(a.Address)
		if !seen[key] {
			seen[key] = true
			out = append(out, a.Address)
		}
	}
	return out, nil
}

// EnvelopeFrom returns the bare sender address.
func (m *Message) EnvelopeFrom() (string, error) {
	a, err := mail.ParseAddress(m.From)
	if err != nil {
		return "", fmt.Errorf("from %q: %w", m.From, err)
	}
	return a.Address, nil
}

// NewMessageID returns a unique Message-ID (with angle brackets) in the sender's domain.
func NewMessageID(from string, now time.Time) string {
	domain := "localhost"
	if a, err := mail.ParseAddress(from); err == nil {
		if i := strings.LastIndex(a.Address, "@"); i >= 0 && i < len(a.Address)-1 {
			domain = a.Address[i+1:]
		}
	}
	var b [12]byte
	_, _ = rand.Read(b[:])
	return fmt.Sprintf("<%d.%s@%s>", now.UnixNano(), hex.EncodeToString(b[:]), domain)
}

// Bytes renders the message with CRLF line endings.
func (m *Message) Bytes() ([]byte, error) {
	from, err := formatAddressList([]string{m.From})
	if err != nil {
		return nil, fmt.Errorf("from: %w", err)
	}
	to, err := formatAddressList(m.To)
	if err != nil {
		return nil, fmt.Errorf("to: %w", err)
	}
	cc, err := formatAddressList(m.Cc)
	if err != nil {
		return nil, fmt.Errorf("cc: %w", err)
	}
	date := m.Date
	if date.IsZero() {
		date = time.Now()
	}
	msgID := m.MessageID
	if msgID == "" {
		msgID = NewMessageID(m.From, date)
	}
	if !strings.HasPrefix(msgID, "<") {
		msgID = "<" + msgID + ">"
	}

	var buf bytes.Buffer
	hdr := func(k, v string) { fmt.Fprintf(&buf, "%s: %s\r\n", k, v) }
	hdr("From", from)
	hdr("To", to)
	if cc != "" {
		hdr("Cc", cc)
	}
	hdr("Subject", EncodeHeader(m.Subject))
	hdr("Date", date.Format(time.RFC1123Z))
	hdr("Message-ID", msgID)
	hdr("MIME-Version", "1.0")

	bodyType := "text/plain"
	if m.HTML {
		bodyType = "text/html"
	}
	bodyType = mime.FormatMediaType(bodyType, map[string]string{"charset": "UTF-8"})

	if len(m.Attachments) == 0 {
		hdr("Content-Type", bodyType)
		hdr("Content-Transfer-Encoding", "quoted-printable")
		buf.WriteString("\r\n")
		if err := writeQP(&buf, m.Body); err != nil {
			return nil, err
		}
		return buf.Bytes(), nil
	}

	mw := multipart.NewWriter(&buf)
	hdr("Content-Type", mime.FormatMediaType("multipart/mixed", map[string]string{"boundary": mw.Boundary()}))
	buf.WriteString("\r\n")

	part, err := mw.CreatePart(textproto.MIMEHeader{
		"Content-Type":              {bodyType},
		"Content-Transfer-Encoding": {"quoted-printable"},
	})
	if err != nil {
		return nil, err
	}
	if err := writeQP(part, m.Body); err != nil {
		return nil, err
	}
	for _, a := range m.Attachments {
		name := filepath.Base(a.Filename)
		ct := a.ContentType
		if ct == "" {
			ct = mime.TypeByExtension(filepath.Ext(name))
		}
		if ct == "" {
			ct = "application/octet-stream"
		}
		if mt, params, err := mime.ParseMediaType(ct); err == nil {
			params["name"] = name
			ct = mime.FormatMediaType(mt, params)
		}
		part, err := mw.CreatePart(textproto.MIMEHeader{
			"Content-Type":              {ct},
			"Content-Transfer-Encoding": {"base64"},
			"Content-Disposition":       {mime.FormatMediaType("attachment", map[string]string{"filename": name})},
		})
		if err != nil {
			return nil, err
		}
		if err := writeBase64Lines(part, a.Data); err != nil {
			return nil, err
		}
	}
	if err := mw.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// EncodeHeader encodes a header value per RFC 2047 when it contains non-ASCII
// characters, folding long values across lines.
func EncodeHeader(s string) string {
	s = strings.NewReplacer("\r", " ", "\n", " ").Replace(s)
	enc := mime.BEncoding.Encode("UTF-8", s)
	if enc == s {
		return s
	}
	// mime.WordEncoder separates encoded-words with a single space; fold there.
	return strings.ReplaceAll(enc, "?= =?", "?=\r\n =?")
}

func formatAddressList(list []string) (string, error) {
	out := make([]string, 0, len(list))
	for _, raw := range list {
		a, err := mail.ParseAddress(raw)
		if err != nil {
			return "", fmt.Errorf("%q: %w", raw, err)
		}
		out = append(out, a.String())
	}
	return strings.Join(out, ", "), nil
}

type writer interface{ Write([]byte) (int, error) }

func writeQP(w writer, s string) error {
	qp := quotedprintable.NewWriter(w)
	if _, err := qp.Write([]byte(s)); err != nil {
		return err
	}
	return qp.Close()
}

func writeBase64Lines(w writer, data []byte) error {
	enc := base64.StdEncoding.EncodeToString(data)
	for len(enc) > 76 {
		if _, err := w.Write([]byte(enc[:76] + "\r\n")); err != nil {
			return err
		}
		enc = enc[76:]
	}
	if enc != "" {
		if _, err := w.Write([]byte(enc + "\r\n")); err != nil {
			return err
		}
	}
	return nil
}
