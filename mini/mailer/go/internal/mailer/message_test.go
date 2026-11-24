package mailer

import (
	"bytes"
	"io"
	"mime"
	"mime/multipart"
	"net/mail"
	"strings"
	"testing"
	"time"
)

func decodeHeader(t *testing.T, s string) string {
	t.Helper()
	out, err := new(mime.WordDecoder).DecodeHeader(s)
	if err != nil {
		t.Fatalf("decode %q: %v", s, err)
	}
	return out
}

func TestPlainMessageHeaders(t *testing.T) {
	subj := "Ежедневный отчёт за 2026-11-19 — очень длинная тема, чтобы проверить перенос строк заголовка"
	m := &Message{
		From:    "Рассылка <bot@example.com>",
		To:      []string{"team@example.com", "boss@example.com"},
		Cc:      []string{"cc@example.com"},
		Subject: subj,
		Body:    "Привет!\nСтрока два.\n",
		Date:    time.Date(2026, 11, 19, 9, 0, 0, 0, time.FixedZone("MSK", 3*3600)),
	}
	raw, err := m.Bytes()
	if err != nil {
		t.Fatal(err)
	}
	for _, line := range strings.Split(string(raw), "\r\n") {
		if len(line) > 998 {
			t.Fatalf("line too long: %d", len(line))
		}
		for _, r := range line {
			if r > 127 {
				t.Fatalf("non-ASCII in raw message line %q", line)
			}
		}
	}
	if bytes.Contains(bytes.ReplaceAll(raw, []byte("\r\n"), nil), []byte("\n")) {
		t.Fatal("bare LF in message")
	}
	msg, err := mail.ReadMessage(bytes.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	h := msg.Header
	if got := decodeHeader(t, h.Get("Subject")); got != subj {
		t.Fatalf("subject = %q", got)
	}
	if !strings.HasPrefix(h.Get("Subject"), "=?UTF-8?b?") {
		t.Fatalf("subject not RFC 2047 encoded: %q", h.Get("Subject"))
	}
	if h.Get("Message-Id") == "" || !strings.HasSuffix(h.Get("Message-Id"), "@example.com>") {
		t.Fatalf("message-id = %q", h.Get("Message-Id"))
	}
	if d, err := h.Date(); err != nil || !d.Equal(m.Date) {
		t.Fatalf("date = %v, %v", d, err)
	}
	from, err := h.AddressList("From")
	if err != nil || from[0].Name != "Рассылка" || from[0].Address != "bot@example.com" {
		t.Fatalf("from = %v, %v", from, err)
	}
	to, _ := h.AddressList("To")
	if len(to) != 2 {
		t.Fatalf("to = %v", to)
	}
	if h.Get("Cc") != "<cc@example.com>" {
		t.Fatalf("cc = %q", h.Get("Cc"))
	}
	if h.Get("Content-Type") != "text/plain; charset=UTF-8" || h.Get("MIME-Version") != "1.0" {
		t.Fatalf("content-type = %q", h.Get("Content-Type"))
	}
	body, _ := io.ReadAll(msg.Body)
	dec, _ := io.ReadAll(newQPReader(body))
	if string(dec) != "Привет!\r\nСтрока два.\r\n" {
		t.Fatalf("body = %q", dec)
	}
}

func TestASCIISubjectNotEncoded(t *testing.T) {
	m := &Message{From: "a@b.c", To: []string{"x@y.z"}, Subject: "Hello", HTML: true}
	raw, err := m.Bytes()
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(raw, []byte("Subject: Hello\r\n")) || !bytes.Contains(raw, []byte("text/html; charset=UTF-8")) {
		t.Fatalf("raw:\n%s", raw)
	}
}

func TestMultipartWithAttachment(t *testing.T) {
	payload := bytes.Repeat([]byte{0, 1, 2, 250, 255}, 100)
	m := &Message{
		From: "a@b.c", To: []string{"x@y.z"}, Subject: "Тема", Body: "Тело",
		Attachments: []Attachment{{Filename: "/tmp/dir/отчёт.pdf", Data: payload}},
	}
	raw, err := m.Bytes()
	if err != nil {
		t.Fatal(err)
	}
	msg, err := mail.ReadMessage(bytes.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	mt, params, err := mime.ParseMediaType(msg.Header.Get("Content-Type"))
	if err != nil || mt != "multipart/mixed" {
		t.Fatalf("content-type %q: %v", mt, err)
	}
	mr := multipart.NewReader(msg.Body, params["boundary"])
	p1, err := mr.NextPart()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(p1.Header.Get("Content-Type"), "text/plain") {
		t.Fatalf("part1 type %q", p1.Header.Get("Content-Type"))
	}
	text, _ := io.ReadAll(p1) // multipart.Reader decodes quoted-printable
	if string(text) != "Тело" {
		t.Fatalf("part1 body %q", text)
	}
	p2, err := mr.NextPart()
	if err != nil {
		t.Fatal(err)
	}
	if p2.FileName() != "отчёт.pdf" {
		t.Fatalf("filename %q", p2.FileName())
	}
	if !strings.HasPrefix(p2.Header.Get("Content-Type"), "application/pdf") {
		t.Fatalf("attachment type %q", p2.Header.Get("Content-Type"))
	}
	data, _ := io.ReadAll(newB64Reader(p2))
	if !bytes.Equal(data, payload) {
		t.Fatal("attachment content mismatch")
	}
	if _, err := mr.NextPart(); err != io.EOF {
		t.Fatalf("expected EOF, got %v", err)
	}
}

func TestBadAddress(t *testing.T) {
	if _, err := (&Message{From: "a@b.c", To: []string{"bad"}}).Bytes(); err == nil {
		t.Fatal("expected error")
	}
}

func TestRecipientsDedup(t *testing.T) {
	m := &Message{To: []string{"A <a@x.y>", "b@x.y"}, Cc: []string{"A@X.Y", "c@x.y"}}
	r, err := m.Recipients()
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(r, ",") != "a@x.y,b@x.y,c@x.y" {
		t.Fatalf("recipients %v", r)
	}
}
