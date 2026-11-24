package report

import (
	"encoding/csv"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"salesreport/internal/sales"
)

func TestMoneyMarshalJSON(t *testing.T) {
	tests := map[float64]string{
		0:            "0.00",
		1234.5:       "1234.50",
		1.005:        "1.00", // binary 1.005 is slightly below 1.005
		2.675000001:  "2.68",
		207149040.5:  "207149040.50",
		-0.001:       "0.00",
		113.34902730: "113.35",
	}
	for v, want := range tests {
		b, err := json.Marshal(Money(v))
		if err != nil {
			t.Fatalf("marshal %v: %v", v, err)
		}
		if string(b) != want {
			t.Errorf("Money(%v) = %s, want %s", v, b, want)
		}
	}
}

func sampleSummary() sales.Summary {
	return sales.Summary{
		ValidRows: 2, SkippedRows: 1, TotalRevenue: 300.123,
		RevenueByMonth:    map[string]float64{"2025-02": 100, "2025-01": 200.123},
		RevenueByRegion:   map[string]float64{"Москва": 100, "Казань": 200.123},
		RevenueByCategory: map[string]float64{"Книги": 300.123},
		TopProducts:       []sales.ProductStat{{Product: "A & B", Revenue: 300.123, Quantity: 3}},
		PlanVsFact:        []sales.PlanFact{{Region: "Казань", Fact: 200.123, Target: 400, CompletionPct: 50.03075}},
	}
}

func TestWriteSummaryStructure(t *testing.T) {
	path := filepath.Join(t.TempDir(), FileSummary)
	if err := WriteSummary(path, sampleSummary()); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)

	// Top-level key order follows the specification.
	keys := []string{"valid_rows", "skipped_rows", "total_revenue", "revenue_by_month",
		"revenue_by_region", "revenue_by_category", "top_products", "plan_vs_fact"}
	last := -1
	for _, k := range keys {
		i := strings.Index(text, `"`+k+`"`)
		if i < 0 || i < last {
			t.Fatalf("key %q missing or out of order in:\n%s", k, text)
		}
		last = i
	}
	// Dictionary keys are sorted.
	if strings.Index(text, `"2025-01"`) > strings.Index(text, `"2025-02"`) {
		t.Error("revenue_by_month keys are not sorted")
	}
	if strings.Index(text, `"Казань"`) > strings.Index(text, `"Москва"`) {
		t.Error("revenue_by_region keys are not sorted")
	}
	for _, want := range []string{`"total_revenue": 300.12`, `"completion_pct": 50.03`, `"A & B"`, `"quantity": 3`} {
		if !strings.Contains(text, want) {
			t.Errorf("summary.json lacks %s:\n%s", want, text)
		}
	}
	var decoded map[string]any
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
}

func TestWriteSkipped(t *testing.T) {
	path := filepath.Join(t.TempDir(), FileSkipped)
	skipped := []sales.Skipped{
		{Line: 5, OrderID: "ORD-1", Reason: sales.ReasonBadDate, Detail: `unparseable date "x, y"`},
		{Line: 9, OrderID: "ORD-1", Reason: sales.ReasonDuplicate, Detail: "dup"},
	}
	if err := WriteSkipped(path, skipped); err != nil {
		t.Fatal(err)
	}
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	recs, err := csv.NewReader(f).ReadAll()
	if err != nil {
		t.Fatal(err)
	}
	if len(recs) != 3 || recs[0][0] != "row" || recs[1][0] != "5" || recs[1][3] != `unparseable date "x, y"` || recs[2][2] != sales.ReasonDuplicate {
		t.Errorf("unexpected csv: %v", recs)
	}
}

func TestWriteHTML(t *testing.T) {
	dir := t.TempDir()
	png := []byte("\x89PNG\r\n\x1a\nfake")
	if err := os.WriteFile(filepath.Join(dir, "a.png"), png, 0o644); err != nil {
		t.Fatal(err)
	}
	s := sampleSummary()
	err := WriteHTML(dir, "sales.xlsx", s, []sales.Skipped{{Line: 3, Reason: sales.ReasonBadPrice}},
		[]ChartSpec{{Title: "График A", File: "a.png"}})
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(dir, FileHTML))
	if err != nil {
		t.Fatal(err)
	}
	html := string(data)
	for _, want := range []string{"data:image/png;base64,", "График A", "300,12 ₽", "Казань", "A &amp; B", "Некорректная цена", "50,0%"} {
		if !strings.Contains(html, want) {
			t.Errorf("report.html lacks %q", want)
		}
	}

	if err := WriteHTML(dir, "x", s, nil, []ChartSpec{{Title: "missing", File: "nope.png"}}); err == nil {
		t.Error("expected error for a missing chart file")
	}
}
