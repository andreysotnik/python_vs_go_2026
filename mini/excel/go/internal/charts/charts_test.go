package charts

import (
	"image/png"
	"math"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"gonum.org/v1/plot/vg"

	"salesreport/internal/sales"
)

func TestMonthLabel(t *testing.T) {
	tests := map[string]string{"2025-01": "янв\n2025", "2025-12": "дек\n2025", "garbage": "garbage", "2025-13": "2025-13"}
	for in, want := range tests {
		if got := monthLabel(in); got != want {
			t.Errorf("monthLabel(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestThousandsTicks(t *testing.T) {
	ticks := thousandsTicks{}.Ticks(0, 52_000_000)
	var labels []string
	for _, tk := range ticks {
		if tk.Label != "" {
			labels = append(labels, tk.Label)
		}
	}
	want := []string{"0", "10 000 000", "20 000 000", "30 000 000", "40 000 000", "50 000 000"}
	if !slices.Equal(labels, want) {
		t.Errorf("labels = %q, want %q", labels, want)
	}
	if got := (thousandsTicks{}).Ticks(5, 5); len(got) != 1 {
		t.Errorf("degenerate range ticks = %+v", got)
	}
}

func TestNiceStep(t *testing.T) {
	tests := map[float64]float64{1: 1, 1.5: 2, 2.2: 2.5, 3: 5, 7: 10, 9_000_000: 10_000_000, 0.03: 0.05}
	for in, want := range tests {
		if got := niceStep(in); math.Abs(got-want) > 1e-12*want {
			t.Errorf("niceStep(%v) = %v, want %v", in, got, want)
		}
	}
}

func TestRingSegmentClosedShape(t *testing.T) {
	pts := ringSegment(vg.Point{}, 5, 10, 0, -1)
	if len(pts) < 6 {
		t.Fatalf("too few points: %d", len(pts))
	}
	if d := pts[0].X - 10; d > 1e-9 || d < -1e-9 {
		t.Errorf("first point %v not on outer radius", pts[0])
	}
	if d := pts[len(pts)-1].X - 5; d > 1e-9 || d < -1e-9 {
		t.Errorf("last point %v not on inner radius", pts[len(pts)-1])
	}
}

func TestRenderAll(t *testing.T) {
	s := sales.Summary{
		TotalRevenue:      600,
		RevenueByMonth:    map[string]float64{"2025-01": 100, "2025-02": 500},
		RevenueByRegion:   map[string]float64{"Москва": 600},
		RevenueByCategory: map[string]float64{"Книги": 550, "Спорт": 50},
		TopProducts:       []sales.ProductStat{{Product: "A", Revenue: 600, Quantity: 1}},
		PlanVsFact:        []sales.PlanFact{{Region: "Москва", Fact: 600, Target: 1000, CompletionPct: 60}},
	}
	for name, sum := range map[string]sales.Summary{"normal": s, "empty": {}} {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			if err := RenderAll(sum, dir); err != nil {
				t.Fatal(err)
			}
			for _, f := range Files {
				fh, err := os.Open(filepath.Join(dir, f))
				if err != nil {
					t.Fatal(err)
				}
				if _, err := png.Decode(fh); err != nil {
					t.Errorf("%s: %v", f, err)
				}
				_ = fh.Close()
			}
		})
	}
}
