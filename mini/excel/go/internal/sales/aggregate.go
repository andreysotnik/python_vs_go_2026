package sales

import (
	"cmp"
	"maps"
	"slices"
)

// TopProductsLimit is the number of products reported in Summary.TopProducts.
const TopProductsLimit = 10

// ProductStat is the revenue and sold quantity of one product.
type ProductStat struct {
	Product  string
	Revenue  float64
	Quantity int
}

// PlanFact compares the yearly fact revenue of a region with its target.
type PlanFact struct {
	Region        string
	Fact          float64
	Target        float64
	CompletionPct float64 // fact / target * 100; 0 when target is 0
}

// Summary holds all aggregates of the report. Amounts are not rounded.
type Summary struct {
	ValidRows         int
	SkippedRows       int
	TotalRevenue      float64
	RevenueByMonth    map[string]float64
	RevenueByRegion   map[string]float64
	RevenueByCategory map[string]float64
	TopProducts       []ProductStat
	PlanVsFact        []PlanFact
}

// sum is a Neumaier compensated accumulator that keeps the floating point
// error of long summations negligible.
type sum struct {
	s, c float64
}

func (a *sum) add(v float64) {
	t := a.s + v
	if abs(a.s) >= abs(v) {
		a.c += (a.s - t) + v
	} else {
		a.c += (v - t) + a.s
	}
	a.s = t
}

func (a *sum) value() float64 { return a.s + a.c }

func abs(v float64) float64 {
	if v < 0 {
		return -v
	}
	return v
}

type sums map[string]*sum

func (m sums) add(key string, v float64) {
	a, ok := m[key]
	if !ok {
		a = &sum{}
		m[key] = a
	}
	a.add(v)
}

func (m sums) values() map[string]float64 {
	out := make(map[string]float64, len(m))
	for k, a := range m {
		out[k] = a.value()
	}
	return out
}

// Summarize computes all report aggregates from cleaned orders, the number
// of skipped rows and the revenue targets.
func Summarize(orders []Order, skipped int, targets []Target) Summary {
	var total sum
	byMonth, byRegion, byCategory, byProduct := sums{}, sums{}, sums{}, sums{}
	qtyByProduct := map[string]int{}

	for _, o := range orders {
		rev := o.Revenue()
		total.add(rev)
		byMonth.add(o.Month(), rev)
		byRegion.add(o.Region, rev)
		byCategory.add(o.Category, rev)
		byProduct.add(o.Product, rev)
		qtyByProduct[o.Product] += o.Quantity
	}

	regionRevenue := byRegion.values()
	return Summary{
		ValidRows:         len(orders),
		SkippedRows:       skipped,
		TotalRevenue:      total.value(),
		RevenueByMonth:    byMonth.values(),
		RevenueByRegion:   regionRevenue,
		RevenueByCategory: byCategory.values(),
		TopProducts:       topProducts(byProduct.values(), qtyByProduct, TopProductsLimit),
		PlanVsFact:        planVsFact(regionRevenue, targets),
	}
}

// topProducts returns the n products with the highest revenue; ties are
// broken by product name (ascending).
func topProducts(revenue map[string]float64, qty map[string]int, n int) []ProductStat {
	stats := make([]ProductStat, 0, len(revenue))
	for p, r := range revenue {
		stats = append(stats, ProductStat{Product: p, Revenue: r, Quantity: qty[p]})
	}
	slices.SortFunc(stats, func(a, b ProductStat) int {
		if c := cmp.Compare(b.Revenue, a.Revenue); c != 0 {
			return c
		}
		return cmp.Compare(a.Product, b.Product)
	})
	if len(stats) > n {
		stats = stats[:n]
	}
	return stats
}

// planVsFact builds the per-region yearly comparison for every region that
// has either sales or targets, ordered by region name.
func planVsFact(fact map[string]float64, targets []Target) []PlanFact {
	plan := sums{}
	for _, t := range targets {
		plan.add(t.Region, t.Amount)
	}
	planTotals := plan.values()

	regions := slices.Collect(maps.Keys(fact))
	for r := range planTotals {
		if _, ok := fact[r]; !ok {
			regions = append(regions, r)
		}
	}
	slices.Sort(regions)

	out := make([]PlanFact, 0, len(regions))
	for _, r := range regions {
		pf := PlanFact{Region: r, Fact: fact[r], Target: planTotals[r]}
		if pf.Target != 0 {
			pf.CompletionPct = pf.Fact / pf.Target * 100
		}
		out = append(out, pf)
	}
	return out
}
