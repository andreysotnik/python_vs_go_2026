// Package report writes the report artefacts: summary.json, skipped.csv and
// report.html.
package report

import (
	"bytes"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"strconv"

	"salesreport/internal/sales"
)

// Output file names.
const (
	FileSummary = "summary.json"
	FileSkipped = "skipped.csv"
	FileHTML    = "report.html"
)

// Money is an amount rounded to 2 decimals when marshalled to JSON. It is
// always written with exactly two fraction digits (e.g. 1234.50, 0.00).
type Money float64

// MarshalJSON implements json.Marshaler.
func (m Money) MarshalJSON() ([]byte, error) {
	v := float64(m)
	if math.IsNaN(v) || math.IsInf(v, 0) {
		return nil, fmt.Errorf("money value is not finite: %v", v)
	}
	r := math.Round(v*100) / 100
	if r == 0 {
		r = 0 // normalise negative zero
	}
	return []byte(strconv.FormatFloat(r, 'f', 2, 64)), nil
}

// ProductJSON is an entry of top_products.
type ProductJSON struct {
	Product  string `json:"product"`
	Revenue  Money  `json:"revenue"`
	Quantity int    `json:"quantity"`
}

// PlanFactJSON is an entry of plan_vs_fact.
type PlanFactJSON struct {
	Region        string `json:"region"`
	Fact          Money  `json:"fact"`
	Target        Money  `json:"target"`
	CompletionPct Money  `json:"completion_pct"`
}

// SummaryJSON mirrors the required summary.json layout. Maps are encoded
// with keys sorted (encoding/json guarantees this).
type SummaryJSON struct {
	ValidRows         int              `json:"valid_rows"`
	SkippedRows       int              `json:"skipped_rows"`
	TotalRevenue      Money            `json:"total_revenue"`
	RevenueByMonth    map[string]Money `json:"revenue_by_month"`
	RevenueByRegion   map[string]Money `json:"revenue_by_region"`
	RevenueByCategory map[string]Money `json:"revenue_by_category"`
	TopProducts       []ProductJSON    `json:"top_products"`
	PlanVsFact        []PlanFactJSON   `json:"plan_vs_fact"`
}

func toMoneyMap(m map[string]float64) map[string]Money {
	out := make(map[string]Money, len(m))
	for k, v := range m {
		out[k] = Money(v)
	}
	return out
}

// NewSummaryJSON converts the aggregates into the JSON representation.
func NewSummaryJSON(s sales.Summary) SummaryJSON {
	out := SummaryJSON{
		ValidRows:         s.ValidRows,
		SkippedRows:       s.SkippedRows,
		TotalRevenue:      Money(s.TotalRevenue),
		RevenueByMonth:    toMoneyMap(s.RevenueByMonth),
		RevenueByRegion:   toMoneyMap(s.RevenueByRegion),
		RevenueByCategory: toMoneyMap(s.RevenueByCategory),
		TopProducts:       make([]ProductJSON, 0, len(s.TopProducts)),
		PlanVsFact:        make([]PlanFactJSON, 0, len(s.PlanVsFact)),
	}
	for _, p := range s.TopProducts {
		out.TopProducts = append(out.TopProducts, ProductJSON{
			Product: p.Product, Revenue: Money(p.Revenue), Quantity: p.Quantity,
		})
	}
	for _, pf := range s.PlanVsFact {
		out.PlanVsFact = append(out.PlanVsFact, PlanFactJSON{
			Region: pf.Region, Fact: Money(pf.Fact), Target: Money(pf.Target),
			CompletionPct: Money(pf.CompletionPct),
		})
	}
	return out
}

// WriteSummary writes summary.json (UTF-8, indented, non-ASCII unescaped).
func WriteSummary(path string, s sales.Summary) error {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(NewSummaryJSON(s)); err != nil {
		return fmt.Errorf("encode summary: %w", err)
	}
	if err := os.WriteFile(path, buf.Bytes(), 0o644); err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	return nil
}

// WriteSkipped writes the skipped rows log as CSV: row,order_id,reason,detail.
func WriteSkipped(path string, skipped []sales.Skipped) (err error) {
	f, err := os.Create(path)
	if err != nil {
		return fmt.Errorf("create %s: %w", path, err)
	}
	defer func() {
		if cerr := f.Close(); cerr != nil && err == nil {
			err = fmt.Errorf("close %s: %w", path, cerr)
		}
	}()
	w := csv.NewWriter(f)
	if err := w.Write([]string{"row", "order_id", "reason", "detail"}); err != nil {
		return err
	}
	for _, s := range skipped {
		if err := w.Write([]string{strconv.Itoa(s.Line), s.OrderID, s.Reason, s.Detail}); err != nil {
			return err
		}
	}
	w.Flush()
	if err := w.Error(); err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	return nil
}
