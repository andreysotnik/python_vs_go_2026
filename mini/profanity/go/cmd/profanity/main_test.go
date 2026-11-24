package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRunCheckJSONL(t *testing.T) {
	path := filepath.Join(t.TempDir(), "in.txt")
	if err := os.WriteFile(path, []byte("Полный пиздец <тут>\nНаша команда\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if err := runCheck([]string{"--input", path}, &out); err != nil {
		t.Fatal(err)
	}
	want := `{"line":1,"text":"Полный пиздец <тут>","profane":true,"matches":["пиздец"]}
{"line":2,"text":"Наша команда","profane":false,"matches":[]}
`
	if out.String() != want {
		t.Errorf("got:\n%s\nwant:\n%s", out.String(), want)
	}
}

func TestRunEval(t *testing.T) {
	var out bytes.Buffer
	if err := runEval([]string{"--input", "../../data/sample.tsv"}, &out); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "F1:") {
		t.Errorf("unexpected eval output:\n%s", out.String())
	}
}
