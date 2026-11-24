// Command profanity is a CLI for the profanity package.
//
//	profanity check  [--input file]   JSONL verdict per input line
//	profanity censor [--input file]   censored text
//	profanity eval   --input file.tsv precision/recall/F1 on "label<TAB>text"
//	profanity bench  [--lines N]      throughput of Check in lines/second
package main

import (
	"bufio"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"profanity/profanity"
)

const usage = `usage:
  profanity check  [--input file]    one JSON object per input line (stdin by default)
  profanity censor [--input file]    print the text with obscene words censored
  profanity eval   --input file.tsv  precision/recall/F1 on "label<TAB>text" data
  profanity bench  [--lines N]       measure Check throughput (lines/second)
`

func main() {
	if len(os.Args) < 2 {
		fmt.Fprint(os.Stderr, usage)
		os.Exit(2)
	}
	var err error
	switch os.Args[1] {
	case "check":
		err = runCheck(os.Args[2:], os.Stdout)
	case "censor":
		err = runCensor(os.Args[2:], os.Stdout)
	case "eval":
		err = runEval(os.Args[2:], os.Stdout)
	case "bench":
		err = runBench(os.Args[2:], os.Stdout)
	case "-h", "--help", "help":
		fmt.Print(usage)
		return
	default:
		fmt.Fprintf(os.Stderr, "unknown command %q\n%s", os.Args[1], usage)
		os.Exit(2)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

// openInput returns the file named by path, or stdin when path is empty.
func openInput(path string) (io.ReadCloser, error) {
	if path == "" {
		return io.NopCloser(os.Stdin), nil
	}
	return os.Open(path)
}

func newScanner(r io.Reader) *bufio.Scanner {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 1024*1024), 64*1024*1024)
	return sc
}

type checkLine struct {
	Line    int      `json:"line"`
	Text    string   `json:"text"`
	Profane bool     `json:"profane"`
	Matches []string `json:"matches"`
}

func runCheck(args []string, w io.Writer) error {
	fs := flag.NewFlagSet("check", flag.ContinueOnError)
	input := fs.String("input", "", "input file (default: stdin)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	in, err := openInput(*input)
	if err != nil {
		return err
	}
	defer in.Close()
	bw := bufio.NewWriter(w)
	defer bw.Flush()
	enc := json.NewEncoder(bw)
	enc.SetEscapeHTML(false)
	sc := newScanner(in)
	n := 0
	for sc.Scan() {
		n++
		text := strings.TrimSuffix(sc.Text(), "\r")
		res := profanity.Check(text)
		words := make([]string, 0, len(res.Matches))
		for _, m := range res.Matches {
			words = append(words, m.Word)
		}
		if err := enc.Encode(checkLine{Line: n, Text: text, Profane: res.Profane, Matches: words}); err != nil {
			return err
		}
	}
	return sc.Err()
}

func runCensor(args []string, w io.Writer) error {
	fs := flag.NewFlagSet("censor", flag.ContinueOnError)
	input := fs.String("input", "", "input file (default: stdin)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	in, err := openInput(*input)
	if err != nil {
		return err
	}
	defer in.Close()
	bw := bufio.NewWriter(w)
	defer bw.Flush()
	sc := newScanner(in)
	for sc.Scan() {
		if _, err := fmt.Fprintln(bw, profanity.Censor(sc.Text())); err != nil {
			return err
		}
	}
	return sc.Err()
}

func runEval(args []string, w io.Writer) error {
	fs := flag.NewFlagSet("eval", flag.ContinueOnError)
	input := fs.String("input", "", "TSV file with label<TAB>text lines")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *input == "" {
		return errors.New("eval: --input is required")
	}
	in, err := openInput(*input)
	if err != nil {
		return err
	}
	defer in.Close()
	var tp, fp, fn, tn, skipped int
	var errs []string
	sc := newScanner(in)
	n := 0
	for sc.Scan() {
		n++
		line := strings.TrimSuffix(sc.Text(), "\r")
		if strings.TrimSpace(line) == "" {
			continue
		}
		label, text, ok := strings.Cut(line, "\t")
		label = strings.TrimSpace(label)
		if !ok || (label != "0" && label != "1") {
			skipped++ // header or malformed line
			continue
		}
		res := profanity.Check(text)
		want := label == "1"
		switch {
		case want && res.Profane:
			tp++
		case !want && !res.Profane:
			tn++
		case want:
			fn++
			errs = append(errs, fmt.Sprintf("FN\tline %d\t%s", n, text))
		default:
			fp++
			words := make([]string, 0, len(res.Matches))
			for _, m := range res.Matches {
				words = append(words, m.Word+"("+m.Root+")")
			}
			errs = append(errs, fmt.Sprintf("FP\tline %d\t%s\t%s", n, text, strings.Join(words, ", ")))
		}
	}
	if err := sc.Err(); err != nil {
		return err
	}
	precision := ratio(tp, tp+fp)
	recall := ratio(tp, tp+fn)
	f1 := 0.0
	if precision+recall > 0 {
		f1 = 2 * precision * recall / (precision + recall)
	}
	fmt.Fprintf(w, "examples:  %d (skipped %d)\n", tp+fp+fn+tn, skipped)
	fmt.Fprintf(w, "TP=%d FP=%d FN=%d TN=%d\n", tp, fp, fn, tn)
	fmt.Fprintf(w, "precision: %.4f\nrecall:    %.4f\nF1:        %.4f\n", precision, recall, f1)
	fmt.Fprintf(w, "errors:    %d\n", len(errs))
	for _, e := range errs {
		fmt.Fprintln(w, "  "+e)
	}
	return nil
}

func ratio(a, b int) float64 {
	if b == 0 {
		return 1
	}
	return float64(a) / float64(b)
}

// benchCorpus is a mix of clean, obscene and obfuscated lines.
var benchCorpus = []string{
	"Наша команда выиграла чемпионат, поздравляем всех болельщиков!",
	"Полный пиздец на дорогах, опять пробка до самого центра",
	"Курс рубля сегодня снова упал, аналитики ждут коррекции",
	"Не еби мозг, просто сделай как договаривались",
	"x у й с ним, завтра разберёмся",
	"Учебник по физике для девятого класса, издание второе",
	"Shit happens, but the shiitake soup was great in Scunthorpe",
	"Застрахуйте имущество до конца месяца со скидкой",
	"бл@ть, опять пи3дец с этим релизом",
	"Мандарины в подарок каждому покупателю, акция до пятницы",
	"Скипидарная мазь помогает при болях в спине",
	"Какой мудак тут припарковался прямо у подъезда",
	"The assassin was a motherfucker, said the potential consumer",
	"Употребление сахара вредно для здоровья, говорят врачи",
	"ЗАЕБАЛО это всё, ухожу в отпуск",
	"Небесно-голубой цвет отлично подходит для детской комнаты",
}

func runBench(args []string, w io.Writer) error {
	fs := flag.NewFlagSet("bench", flag.ContinueOnError)
	lines := fs.Int("lines", 100000, "number of lines to check")
	if err := fs.Parse(args); err != nil {
		return err
	}
	profane := 0
	start := time.Now()
	for i := 0; i < *lines; i++ {
		if profanity.Check(benchCorpus[i%len(benchCorpus)]).Profane {
			profane++
		}
	}
	el := time.Since(start)
	fmt.Fprintf(w, "lines: %d, profane: %d, time: %s, throughput: %.0f lines/s\n",
		*lines, profane, el.Round(time.Millisecond), float64(*lines)/el.Seconds())
	return nil
}
