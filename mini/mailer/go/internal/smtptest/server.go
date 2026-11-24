// Package smtptest provides an in-process fake SMTP server for tests.
package smtptest

import (
	"bufio"
	"bytes"
	"fmt"
	"net"
	"strings"
	"sync"
	"testing"
)

// Received is one accepted message.
type Received struct {
	From string
	Rcpt []string
	Data []byte
}

// Reply overrides the server's answer for a command.
type Reply struct {
	Code int
	Text string
}

// HookFunc may override the reply for a command. conn is the 1-based
// connection number; verb is the upper-cased SMTP verb, "CONNECT" for the
// greeting, or "DATA_END" for the reply after the message body.
// Returning nil keeps the default reply.
type HookFunc func(conn int, verb string) *Reply

// Server is a minimal SMTP server (no TLS, no AUTH).
type Server struct {
	ln   net.Listener
	hook HookFunc

	mu       sync.Mutex
	conns    int
	messages []Received
	wg       sync.WaitGroup
}

// Start launches the server on a random localhost port; it stops on test cleanup.
func Start(t testing.TB, hook HookFunc) *Server {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	s := &Server{ln: ln, hook: hook}
	s.wg.Add(1)
	go s.serve()
	t.Cleanup(s.Close)
	return s
}

// Addr returns host and port.
func (s *Server) Addr() (string, int) {
	a := s.ln.Addr().(*net.TCPAddr)
	return a.IP.String(), a.Port
}

// Close stops the server and waits for connections to finish.
func (s *Server) Close() {
	_ = s.ln.Close()
	s.wg.Wait()
}

// Messages returns a copy of accepted messages.
func (s *Server) Messages() []Received {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]Received(nil), s.messages...)
}

// Connections returns how many connections were accepted.
func (s *Server) Connections() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.conns
}

func (s *Server) serve() {
	defer s.wg.Done()
	for {
		c, err := s.ln.Accept()
		if err != nil {
			return
		}
		s.mu.Lock()
		s.conns++
		n := s.conns
		s.mu.Unlock()
		s.wg.Add(1)
		go func() {
			defer s.wg.Done()
			defer func() { _ = c.Close() }()
			s.handle(c, n)
		}()
	}
}

func (s *Server) reply(conn int, verb string, def Reply) Reply {
	if s.hook != nil {
		if r := s.hook(conn, verb); r != nil {
			return *r
		}
	}
	return def
}

func (s *Server) handle(c net.Conn, n int) {
	r := bufio.NewReader(c)
	w := bufio.NewWriter(c)
	send := func(rep Reply) bool {
		_, _ = fmt.Fprintf(w, "%d %s\r\n", rep.Code, rep.Text)
		return w.Flush() == nil
	}
	greet := s.reply(n, "CONNECT", Reply{220, "fake ESMTP ready"})
	if !send(greet) || greet.Code >= 400 {
		return
	}
	var cur Received
	for {
		line, err := r.ReadString('\n')
		if err != nil {
			return
		}
		line = strings.TrimRight(line, "\r\n")
		verb, arg, _ := strings.Cut(line, " ")
		verb = strings.ToUpper(verb)
		switch verb {
		case "EHLO":
			rep := s.reply(n, verb, Reply{250, "fake"})
			if rep.Code == 250 {
				_, _ = fmt.Fprintf(w, "250-fake greets %s\r\n250 8BITMIME\r\n", arg)
				if w.Flush() != nil {
					return
				}
				continue
			}
			send(rep)
		case "HELO":
			send(s.reply(n, verb, Reply{250, "fake"}))
		case "MAIL":
			rep := s.reply(n, verb, Reply{250, "OK"})
			if rep.Code == 250 {
				cur = Received{From: extractAddr(arg)}
			}
			send(rep)
		case "RCPT":
			rep := s.reply(n, verb, Reply{250, "OK"})
			if rep.Code == 250 {
				cur.Rcpt = append(cur.Rcpt, extractAddr(arg))
			}
			send(rep)
		case "DATA":
			rep := s.reply(n, verb, Reply{354, "go ahead"})
			if !send(rep) {
				return
			}
			if rep.Code != 354 {
				continue
			}
			var body bytes.Buffer
			for {
				l, err := r.ReadString('\n')
				if err != nil {
					return
				}
				if l == ".\r\n" {
					break
				}
				l = strings.TrimPrefix(l, ".")
				body.WriteString(l)
			}
			end := s.reply(n, "DATA_END", Reply{250, "queued"})
			if end.Code == 250 {
				cur.Data = body.Bytes()
				s.mu.Lock()
				s.messages = append(s.messages, cur)
				s.mu.Unlock()
			}
			cur = Received{}
			send(end)
		case "RSET":
			cur = Received{}
			send(Reply{250, "OK"})
		case "NOOP":
			send(Reply{250, "OK"})
		case "QUIT":
			send(Reply{221, "bye"})
			return
		default:
			send(Reply{502, "not implemented"})
		}
	}
}

func extractAddr(arg string) string {
	i := strings.Index(arg, "<")
	j := strings.LastIndex(arg, ">")
	if i >= 0 && j > i {
		return arg[i+1 : j]
	}
	return arg
}
