package sales

import (
	"fmt"
	"math"
	"testing"
	"time"
)

func order(month int, region, category, product string, qty int, price, discount float64) Order {
	return Order{
		Date: day(time.Month(month), 10), Region: region, Category: category,
		Product: product, Quantity: qty, UnitPrice: price, Discount: discount,
	}
}

func approx(t *testing.T, name string, got, want float64) {
	t.Helper()
	if math.Abs(got-want) > 1e-6 {
		t.Errorf("%s = %v, want %v", name, got, want)
	}
}

func TestSummarizeTotalsAndGroups(t *testing.T) {
	orders := []Order{
		order(1, "Москва", "Книги", "A", 2, 100, 0),        // 200
		order(1, "Казань", "Книги", "B", 1, 1234.5, 0.1),   // 1111.05
		order(2, "Москва", "Спорт", "A", 3, 50, 0.5),       // 75
		order(12, "Казань", "Электроника", "C", 1, 999, 0), // 999
	}
	s := Summarize(orders, 7, nil)
	if s.ValidRows != 4 || s.SkippedRows != 7 {
		t.Errorf("rows = %d/%d, want 4/7", s.ValidRows, s.SkippedRows)
	}
	approx(t, "total", s.TotalRevenue, 200+1111.05+75+999)
	approx(t, "2025-01", s.RevenueByMonth["2025-01"], 1311.05)
	approx(t, "2025-02", s.RevenueByMonth["2025-02"], 75)
	approx(t, "2025-12", s.RevenueByMonth["2025-12"], 999)
	approx(t, "Москва", s.RevenueByRegion["Москва"], 275)
	approx(t, "Казань", s.RevenueByRegion["Казань"], 2110.05)
	approx(t, "Книги", s.RevenueByCategory["Книги"], 1311.05)
	if len(s.RevenueByMonth) != 3 || len(s.RevenueByRegion) != 2 || len(s.RevenueByCategory) != 3 {
		t.Errorf("unexpected group sizes: %v %v %v", s.RevenueByMonth, s.RevenueByRegion, s.RevenueByCategory)
	}
}

func TestSummarizeFullPrecision(t *testing.T) {
	// 0.1 summed 10 times is 0.9999999999999999 with naive float addition;
	// the compensated sum must give exactly 1.
	var orders []Order
	for range 10 {
		orders = append(orders, order(1, "R", "C", "P", 1, 0.1, 0))
	}
	s := Summarize(orders, 0, nil)
	if s.TotalRevenue != 1 {
		t.Errorf("total = %.17g, want 1", s.TotalRevenue)
	}
}

func TestTopProductsOrderingAndLimit(t *testing.T) {
	var orders []Order
	// 12 products; P00..P11 with revenue 100*(i+1), plus a tie.
	for i := range 12 {
		orders = append(orders, order(1, "R", "C", fmt.Sprintf("P%02d", i), i+1, 100, 0))
	}
	orders = append(orders, order(1, "R", "C", "AAA", 12, 100, 0)) // ties with P11
	orders = append(orders, order(2, "R", "C", "P11", 0, 100, 0))  // qty 0 adds nothing
	s := Summarize(orders, 0, nil)

	if len(s.TopProducts) != TopProductsLimit {
		t.Fatalf("len = %d, want %d", len(s.TopProducts), TopProductsLimit)
	}
	if s.TopProducts[0].Product != "AAA" || s.TopProducts[1].Product != "P11" {
		t.Errorf("tie must be broken by name: got %s, %s", s.TopProducts[0].Product, s.TopProducts[1].Product)
	}
	if s.TopProducts[0].Quantity != 12 || s.TopProducts[0].Revenue != 1200 {
		t.Errorf("unexpected stat %+v", s.TopProducts[0])
	}
	for i := 1; i < len(s.TopProducts); i++ {
		if s.TopProducts[i].Revenue > s.TopProducts[i-1].Revenue {
			t.Errorf("not sorted at %d: %+v", i, s.TopProducts)
		}
	}
	if last := s.TopProducts[9].Product; last != "P03" {
		t.Errorf("last product = %s, want P03", last)
	}
}

func TestTopProductsAggregatesQuantity(t *testing.T) {
	orders := []Order{
		order(1, "R", "C", "X", 2, 10, 0),
		order(2, "R", "C", "X", 3, 10, 0.5),
	}
	s := Summarize(orders, 0, nil)
	if len(s.TopProducts) != 1 || s.TopProducts[0].Quantity != 5 || s.TopProducts[0].Revenue != 35 {
		t.Errorf("got %+v", s.TopProducts)
	}
}

func TestPlanVsFact(t *testing.T) {
	orders := []Order{
		order(1, "Москва", "C", "P", 1, 1000, 0),
		order(2, "Москва", "C", "P", 1, 500, 0),
		order(1, "Казань", "C", "P", 1, 300, 0),
		order(1, "Тверь", "C", "P", 1, 10, 0), // no target
	}
	targets := []Target{
		{Region: "Москва", Month: "2025-01", Amount: 1000},
		{Region: "Москва", Month: "2025-02", Amount: 1000},
		{Region: "Казань", Month: "2025-01", Amount: 600},
		{Region: "Омск", Month: "2025-01", Amount: 100}, // no sales
	}
	s := Summarize(orders, 0, targets)
	wantRegions := []string{"Казань", "Москва", "Омск", "Тверь"}
	if len(s.PlanVsFact) != len(wantRegions) {
		t.Fatalf("plan_vs_fact = %+v", s.PlanVsFact)
	}
	for i, r := range wantRegions {
		if s.PlanVsFact[i].Region != r {
			t.Errorf("region[%d] = %s, want %s", i, s.PlanVsFact[i].Region, r)
		}
	}
	m := s.PlanVsFact[1]
	approx(t, "Москва fact", m.Fact, 1500)
	approx(t, "Москва target", m.Target, 2000)
	approx(t, "Москва pct", m.CompletionPct, 75)
	approx(t, "Казань pct", s.PlanVsFact[0].CompletionPct, 50)
	approx(t, "Омск pct", s.PlanVsFact[2].CompletionPct, 0)
	if tv := s.PlanVsFact[3]; tv.Target != 0 || tv.CompletionPct != 0 || tv.Fact != 10 {
		t.Errorf("region without target = %+v", tv)
	}
}

func TestSummarizeEmpty(t *testing.T) {
	s := Summarize(nil, 3, nil)
	if s.ValidRows != 0 || s.SkippedRows != 3 || s.TotalRevenue != 0 ||
		len(s.TopProducts) != 0 || len(s.PlanVsFact) != 0 || s.RevenueByMonth == nil {
		t.Errorf("unexpected empty summary %+v", s)
	}
}
