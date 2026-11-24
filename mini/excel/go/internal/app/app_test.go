package app

import (
	"bytes"
	"encoding/json"
	"errors"
	"image/png"
	"io"
	"log/slog"
	"math"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"salesreport/internal/charts"
	"salesreport/internal/report"
)

const sampleFile = "../../data/sales.xlsx"

// Integration test: full run on data/sales.xlsx.
func TestRunOnSampleWorkbook(t *testing.T) {
	if _, err := os.Stat(sampleFile); err != nil {
		t.Skipf("sample workbook not available: %v", err)
	}
	out := t.TempDir()
	var logs bytes.Buffer
	log := slog.New(slog.NewTextHandler(&logs, nil))
	if _, err := Run(Config{Input: sampleFile, OutDir: out}, log); err != nil {
		t.Fatalf("Run: %v\nlogs:\n%s", err, logs.String())
	}

	// summary.json: exact key set and types.
	data, err := os.ReadFile(filepath.Join(out, report.FileSummary))
	if err != nil {
		t.Fatal(err)
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatalf("summary.json is not valid JSON: %v", err)
	}
	wantKeys := []string{"plan_vs_fact", "revenue_by_category", "revenue_by_month", "revenue_by_region",
		"skipped_rows", "top_products", "total_revenue", "valid_rows"}
	var gotKeys []string
	for k := range raw {
		gotKeys = append(gotKeys, k)
	}
	slices.Sort(gotKeys)
	if !slices.Equal(gotKeys, wantKeys) {
		t.Fatalf("summary keys = %v, want %v", gotKeys, wantKeys)
	}

	var s struct {
		ValidRows         int                `json:"valid_rows"`
		SkippedRows       int                `json:"skipped_rows"`
		TotalRevenue      float64            `json:"total_revenue"`
		RevenueByMonth    map[string]float64 `json:"revenue_by_month"`
		RevenueByRegion   map[string]float64 `json:"revenue_by_region"`
		RevenueByCategory map[string]float64 `json:"revenue_by_category"`
		TopProducts       []struct {
			Product  string  `json:"product"`
			Revenue  float64 `json:"revenue"`
			Quantity int     `json:"quantity"`
		} `json:"top_products"`
		PlanVsFact []struct {
			Region        string  `json:"region"`
			Fact          float64 `json:"fact"`
			Target        float64 `json:"target"`
			CompletionPct float64 `json:"completion_pct"`
		} `json:"plan_vs_fact"`
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&s); err != nil {
		t.Fatalf("decode summary: %v", err)
	}

	// Data set facts: 5048 data rows, 15 fully empty, 33 duplicates,
	// 33 bad dates, 101 bad quantities, 33 bad prices.
	if s.ValidRows != 4833 || s.SkippedRows != 200 {
		t.Errorf("valid/skipped = %d/%d, want 4833/200", s.ValidRows, s.SkippedRows)
	}
	if len(s.RevenueByMonth) != 12 || len(s.RevenueByRegion) != 5 || len(s.RevenueByCategory) != 5 {
		t.Errorf("group sizes: months=%d regions=%d categories=%d",
			len(s.RevenueByMonth), len(s.RevenueByRegion), len(s.RevenueByCategory))
	}
	for r := range s.RevenueByRegion {
		if r != strings.TrimSpace(r) {
			t.Errorf("region %q is not trimmed", r)
		}
	}
	for name, m := range map[string]map[string]float64{
		"month": s.RevenueByMonth, "region": s.RevenueByRegion, "category": s.RevenueByCategory,
	} {
		total := 0.0
		for _, v := range m {
			total += v
		}
		if math.Abs(total-s.TotalRevenue) > 0.05 {
			t.Errorf("sum by %s = %.2f, total = %.2f", name, total, s.TotalRevenue)
		}
	}
	if len(s.TopProducts) != 10 {
		t.Errorf("top_products has %d entries", len(s.TopProducts))
	}
	for i := 1; i < len(s.TopProducts); i++ {
		a, b := s.TopProducts[i-1], s.TopProducts[i]
		if a.Revenue < b.Revenue || (a.Revenue == b.Revenue && a.Product > b.Product) {
			t.Errorf("top_products not ordered at %d", i)
		}
	}
	if len(s.PlanVsFact) != 5 {
		t.Fatalf("plan_vs_fact has %d entries", len(s.PlanVsFact))
	}
	for i, pf := range s.PlanVsFact {
		if i > 0 && s.PlanVsFact[i-1].Region > pf.Region {
			t.Errorf("plan_vs_fact not sorted by region")
		}
		if pf.Fact != s.RevenueByRegion[pf.Region] {
			t.Errorf("%s fact %.2f != revenue_by_region %.2f", pf.Region, pf.Fact, s.RevenueByRegion[pf.Region])
		}
		if pf.Target <= 0 || math.Abs(pf.Fact/pf.Target*100-pf.CompletionPct) > 0.01 {
			t.Errorf("bad completion for %+v", pf)
		}
	}

	// All charts exist and are valid PNG images.
	for _, name := range charts.Files {
		f, err := os.Open(filepath.Join(out, name))
		if err != nil {
			t.Errorf("chart %s: %v", name, err)
			continue
		}
		img, err := png.Decode(f)
		_ = f.Close()
		if err != nil {
			t.Errorf("chart %s is not a PNG: %v", name, err)
			continue
		}
		if b := img.Bounds(); b.Dx() < 800 || b.Dy() < 400 {
			t.Errorf("chart %s is too small: %v", name, b)
		}
	}

	// report.html and skipped.csv.
	html, err := os.ReadFile(filepath.Join(out, report.FileHTML))
	if err != nil {
		t.Fatal(err)
	}
	if n := strings.Count(string(html), "data:image/png;base64,"); n != len(charts.Files) {
		t.Errorf("report.html embeds %d charts, want %d", n, len(charts.Files))
	}
	skipped, err := os.ReadFile(filepath.Join(out, report.FileSkipped))
	if err != nil {
		t.Fatal(err)
	}
	if lines := strings.Count(string(skipped), "\n"); lines != s.SkippedRows+1 {
		t.Errorf("skipped.csv has %d lines, want %d", lines, s.SkippedRows+1)
	}
}

func TestParseArgs(t *testing.T) {
	cfg, err := ParseArgs([]string{"--input", "a.xlsx", "--out", "dir/"}, io.Discard)
	if err != nil || cfg.Input != "a.xlsx" || cfg.OutDir != "dir/" {
		t.Errorf("cfg = %+v, err = %v", cfg, err)
	}
	for _, args := range [][]string{{}, {"--out", "x"}, {"--input", "a", "extra"}, {"--bogus"}} {
		if _, err := ParseArgs(args, io.Discard); !errors.Is(err, ErrUsage) {
			t.Errorf("ParseArgs(%v) error = %v, want ErrUsage", args, err)
		}
	}
}

func TestRunMissingInput(t *testing.T) {
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	_, err := Run(Config{Input: filepath.Join(t.TempDir(), "none.xlsx"), OutDir: t.TempDir()}, log)
	if err == nil {
		t.Fatal("expected error")
	}
}
