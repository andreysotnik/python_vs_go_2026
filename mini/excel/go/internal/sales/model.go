// Package sales contains the domain model, cleaning rules and aggregations
// for the sales report.
package sales

import "time"

// RawRow is a row of the Sales sheet as read from the workbook, before any
// validation. All values are raw cell contents (numbers as their textual
// representation, e.g. Excel date serials as "45668").
type RawRow struct {
	Line      int // 1-based row number in the sheet (header is line 1)
	OrderID   string
	OrderDate string
	Region    string
	Category  string
	Product   string
	Manager   string
	Quantity  string
	UnitPrice string
	Discount  string
}

// IsEmpty reports whether every cell of the row is blank.
func (r RawRow) IsEmpty() bool {
	for _, v := range []string{
		r.OrderID, r.OrderDate, r.Region, r.Category, r.Product,
		r.Manager, r.Quantity, r.UnitPrice, r.Discount,
	} {
		if trimSpace(v) != "" {
			return false
		}
	}
	return true
}

// Order is a validated, normalised sales row.
type Order struct {
	Line      int
	OrderID   string
	Date      time.Time
	Region    string
	Category  string
	Product   string
	Manager   string
	Quantity  int
	UnitPrice float64
	Discount  float64
}

// Revenue returns quantity * unit_price * (1 - discount) without rounding.
func (o Order) Revenue() float64 {
	return float64(o.Quantity) * o.UnitPrice * (1 - o.Discount)
}

// Month returns the order month in YYYY-MM format.
func (o Order) Month() string {
	return o.Date.Format("2006-01")
}

// Skipped describes a row that was rejected during cleaning.
type Skipped struct {
	Line    int    // 1-based row number in the sheet
	OrderID string // may be empty
	Reason  string // one of the Reason* constants
	Detail  string // human readable explanation
}

// Target is a revenue plan for a region and month.
type Target struct {
	Region string
	Month  string // YYYY-MM
	Amount float64
}
