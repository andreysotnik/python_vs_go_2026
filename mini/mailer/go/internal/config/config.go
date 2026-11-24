// Package config loads and validates the jobs file and SMTP environment.
package config

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"net/mail"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/example/mailer/internal/schedule"
)

// Job is a single scheduled mailing as written in jobs.yaml.
type Job struct {
	Name        string   `yaml:"name"`
	Schedule    string   `yaml:"schedule"`
	Timezone    string   `yaml:"timezone"`
	To          []string `yaml:"to"`
	Cc          []string `yaml:"cc"`
	Subject     string   `yaml:"subject"`
	Body        string   `yaml:"body"`
	HTML        bool     `yaml:"html"`
	Attachments []string `yaml:"attachments"`

	// Resolved fields, filled by Load.
	Sched           *schedule.Schedule `yaml:"-"`
	AttachmentPaths []string           `yaml:"-"`
}

// Location returns the job's time zone (UTC when not set).
func (j *Job) Location() *time.Location {
	if j.Sched == nil {
		return time.UTC
	}
	return j.Sched.Location()
}

// Config is the whole jobs file.
type Config struct {
	Jobs []*Job `yaml:"jobs"`
}

// ValidationError aggregates every problem found in the config.
type ValidationError struct {
	Problems []string
}

func (e *ValidationError) Error() string {
	return fmt.Sprintf("config has %d problem(s):\n  - %s", len(e.Problems), strings.Join(e.Problems, "\n  - "))
}

var jobNameRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*$`)

// Load reads, parses and validates the config file. Relative attachment paths
// are resolved against the directory of the config file.
func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read config: %w", err)
	}
	return Parse(data, filepath.Dir(path))
}

// Parse parses and validates config bytes; baseDir is used for relative attachments.
func Parse(data []byte, baseDir string) (*Config, error) {
	var cfg Config
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	if err := dec.Decode(&cfg); err != nil {
		if errors.Is(err, io.EOF) {
			return nil, &ValidationError{Problems: []string{"config is empty"}}
		}
		return nil, fmt.Errorf("parse config: %w", err)
	}
	if err := cfg.validate(baseDir); err != nil {
		return nil, err
	}
	return &cfg, nil
}

func (c *Config) validate(baseDir string) error {
	var problems []string
	add := func(format string, args ...any) { problems = append(problems, fmt.Sprintf(format, args...)) }

	if len(c.Jobs) == 0 {
		add("no jobs defined")
	}
	seen := map[string]bool{}
	for i, j := range c.Jobs {
		if j == nil {
			add("jobs[%d]: empty job", i)
			continue
		}
		id := fmt.Sprintf("jobs[%d]", i)
		if j.Name != "" {
			id = fmt.Sprintf("job %q", j.Name)
		}
		switch {
		case j.Name == "":
			add("%s: name is required", id)
		case !jobNameRe.MatchString(j.Name):
			add("%s: name must match %s", id, jobNameRe)
		case seen[j.Name]:
			add("%s: duplicate job name", id)
		}
		seen[j.Name] = true

		// Report cron and time zone problems independently.
		_, tzErr := schedule.LoadLocation(j.Timezone)
		if tzErr != nil {
			add("%s: %v", id, tzErr)
		}
		if strings.TrimSpace(j.Schedule) == "" {
			add("%s: schedule is required", id)
		} else if _, err := schedule.Parse(j.Schedule, ""); err != nil {
			add("%s: %v", id, err)
		} else if tzErr == nil {
			j.Sched, _ = schedule.Parse(j.Schedule, j.Timezone)
		}

		if len(j.To) == 0 {
			add("%s: at least one recipient in 'to' is required", id)
		}
		for _, a := range j.To {
			if err := ValidateAddress(a); err != nil {
				add("%s: to: %v", id, err)
			}
		}
		for _, a := range j.Cc {
			if err := ValidateAddress(a); err != nil {
				add("%s: cc: %v", id, err)
			}
		}
		if strings.TrimSpace(j.Subject) == "" {
			add("%s: subject is required", id)
		}

		j.AttachmentPaths = nil
		for _, a := range j.Attachments {
			p := a
			if !filepath.IsAbs(p) {
				p = filepath.Join(baseDir, p)
			}
			st, err := os.Stat(p)
			switch {
			case err != nil:
				add("%s: attachment %q: %v", id, a, err)
			case st.IsDir():
				add("%s: attachment %q is a directory", id, a)
			default:
				f, err := os.Open(p)
				if err != nil {
					add("%s: attachment %q is not readable: %v", id, a, err)
				} else {
					_ = f.Close()
				}
			}
			j.AttachmentPaths = append(j.AttachmentPaths, p)
		}
	}
	if len(problems) > 0 {
		return &ValidationError{Problems: problems}
	}
	return nil
}

// ValidateAddress checks that s is a single bare RFC 5322 address with a domain.
func ValidateAddress(s string) error {
	a, err := mail.ParseAddress(s)
	if err != nil {
		return fmt.Errorf("invalid address %q: %w", s, err)
	}
	at := strings.LastIndex(a.Address, "@")
	if at <= 0 || at == len(a.Address)-1 {
		return fmt.Errorf("invalid address %q", s)
	}
	return nil
}

// TLSMode is the SMTP transport security mode.
type TLSMode string

// Supported TLS modes.
const (
	TLSNone     TLSMode = "none"
	TLSStartTLS TLSMode = "starttls"
	TLSImplicit TLSMode = "tls"
)

// SMTP holds SMTP connection settings.
type SMTP struct {
	Host     string
	Port     int
	User     string
	Password string
	From     string
	TLS      TLSMode
}

// Addr returns host:port.
func (s SMTP) Addr() string { return fmt.Sprintf("%s:%d", s.Host, s.Port) }

// SMTPFromEnv reads SMTP_* variables using getenv (os.Getenv in production).
func SMTPFromEnv(getenv func(string) string) (SMTP, error) {
	var problems []string
	s := SMTP{
		Host:     strings.TrimSpace(getenv("SMTP_HOST")),
		User:     getenv("SMTP_USER"),
		Password: getenv("SMTP_PASSWORD"),
		From:     strings.TrimSpace(getenv("SMTP_FROM")),
		TLS:      TLSMode(strings.ToLower(strings.TrimSpace(getenv("SMTP_TLS")))),
	}
	if s.Host == "" {
		problems = append(problems, "SMTP_HOST is required")
	}
	if s.TLS == "" {
		s.TLS = TLSStartTLS
	}
	switch s.TLS {
	case TLSNone, TLSStartTLS, TLSImplicit:
	default:
		problems = append(problems, fmt.Sprintf("SMTP_TLS must be one of none|starttls|tls, got %q", s.TLS))
	}
	port := strings.TrimSpace(getenv("SMTP_PORT"))
	switch {
	case port == "" && s.TLS == TLSImplicit:
		s.Port = 465
	case port == "" && s.TLS == TLSStartTLS:
		s.Port = 587
	case port == "":
		s.Port = 25
	default:
		p, err := strconv.Atoi(port)
		if err != nil || p <= 0 || p > 65535 {
			problems = append(problems, fmt.Sprintf("SMTP_PORT must be 1..65535, got %q", port))
		}
		s.Port = p
	}
	if s.From == "" {
		problems = append(problems, "SMTP_FROM is required")
	} else if err := ValidateAddress(s.From); err != nil {
		problems = append(problems, "SMTP_FROM: "+err.Error())
	}
	if s.Password != "" && s.User == "" {
		problems = append(problems, "SMTP_PASSWORD is set but SMTP_USER is empty")
	}
	if len(problems) > 0 {
		return SMTP{}, &ValidationError{Problems: problems}
	}
	return s, nil
}
