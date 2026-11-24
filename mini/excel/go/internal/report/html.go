package report

import (
	"bytes"
	_ "embed"
	"encoding/base64"
	"fmt"
	"html/template"
	"os"
	"path/filepath"
	"slices"
	"time"

	"salesreport/internal/numfmt"
	"salesreport/internal/sales"
)

//go:embed report.html.tmpl
var htmlTemplate string

var tmpl = template.Must(template.New("report").Funcs(template.FuncMap{
	"money":  numfmt.Money,
	"num":    func(v int) string { return numfmt.Thousands(float64(v), 0) },
	"pct":    func(v float64) string { return numfmt.Thousands(v, 1) + "%" },
	"reason": reasonText,
}).Parse(htmlTemplate))

// Chart is a chart image embedded in the HTML page.
type Chart struct {
	Title string
	File  string
	Data  template.URL // data: URI of the PNG
}

// ReasonCount is the number of skipped rows per reason.
type ReasonCount struct {
	Reason string
	Count  int
}

type htmlData struct {
	Generated   string
	Source      string
	Summary     sales.Summary
	TotalPlan   float64
	TotalPct    float64
	BestMonth   string
	BestRegion  string
	Charts      []Chart
	SkipReasons []ReasonCount
}

// ChartSpec names a chart file and its caption.
type ChartSpec struct {
	Title string
	File  string
}

// WriteHTML renders report.html into outDir. Chart PNGs are read from
// outDir and embedded as data URIs so the page is self-contained.
func WriteHTML(outDir, source string, s sales.Summary, skipped []sales.Skipped, charts []ChartSpec) error {
	d := htmlData{
		Generated:   time.Now().Format("02.01.2006 15:04"),
		Source:      source,
		Summary:     s,
		BestMonth:   argmax(s.RevenueByMonth),
		BestRegion:  argmax(s.RevenueByRegion),
		SkipReasons: countReasons(skipped),
	}
	for _, pf := range s.PlanVsFact {
		d.TotalPlan += pf.Target
	}
	if d.TotalPlan != 0 {
		d.TotalPct = s.TotalRevenue / d.TotalPlan * 100
	}
	for _, c := range charts {
		png, err := os.ReadFile(filepath.Join(outDir, c.File))
		if err != nil {
			return fmt.Errorf("embed chart: %w", err)
		}
		d.Charts = append(d.Charts, Chart{
			Title: c.Title,
			File:  c.File,
			// #nosec G203 -- the URI is built from our own PNG bytes.
			Data: template.URL("data:image/png;base64," + base64.StdEncoding.EncodeToString(png)),
		})
	}

	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, d); err != nil {
		return fmt.Errorf("render html: %w", err)
	}
	path := filepath.Join(outDir, FileHTML)
	if err := os.WriteFile(path, buf.Bytes(), 0o644); err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	return nil
}

var reasonTexts = map[string]string{
	sales.ReasonDuplicate:       "Повтор order_id",
	sales.ReasonMissingOrderID:  "Пустой order_id",
	sales.ReasonBadDate:         "Некорректная дата",
	sales.ReasonBadQuantity:     "Некорректное количество",
	sales.ReasonBadPrice:        "Некорректная цена",
	sales.ReasonBadDiscount:     "Некорректная скидка",
	sales.ReasonMissingRegion:   "Пустой регион",
	sales.ReasonMissingCategory: "Пустая категория",
	sales.ReasonMissingProduct:  "Пустой товар",
}

func reasonText(code string) string {
	if t, ok := reasonTexts[code]; ok {
		return t + " (" + code + ")"
	}
	return code
}

func argmax(m map[string]float64) string {
	best, bestV := "", 0.0
	for k, v := range m {
		if best == "" || v > bestV || (v == bestV && k < best) {
			best, bestV = k, v
		}
	}
	return best
}

func countReasons(skipped []sales.Skipped) []ReasonCount {
	counts := map[string]int{}
	for _, s := range skipped {
		counts[s.Reason]++
	}
	out := make([]ReasonCount, 0, len(counts))
	for r, c := range counts {
		out = append(out, ReasonCount{Reason: r, Count: c})
	}
	slices.SortFunc(out, func(a, b ReasonCount) int {
		if a.Count != b.Count {
			return b.Count - a.Count
		}
		if a.Reason < b.Reason {
			return -1
		}
		return 1
	})
	return out
}
