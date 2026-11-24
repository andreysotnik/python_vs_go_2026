package sales

import (
	"testing"
)

// validRaw returns a fully valid raw row; tests tweak single fields.
func validRaw(line int, id string) RawRow {
	return RawRow{
		Line: line, OrderID: id, OrderDate: "45668", Region: "Москва",
		Category: "Книги", Product: "Книга", Manager: "Иванов И.",
		Quantity: "2", UnitPrice: "100", Discount: "0.1",
	}
}

func reasons(s []Skipped) []string {
	out := make([]string, len(s))
	for i, x := range s {
		out[i] = x.Reason
	}
	return out
}

// Rule 1: fully empty rows are neither valid nor skipped.
func TestCleanIgnoresEmptyRows(t *testing.T) {
	rows := []RawRow{
		validRaw(2, "A"),
		{Line: 3},
		{Line: 4, Region: "   ", Discount: "\t"},
		validRaw(5, "B"),
	}
	res := Clean(rows)
	if len(res.Orders) != 2 || len(res.Skipped) != 0 {
		t.Fatalf("got %d valid, %d skipped; want 2, 0 (%+v)", len(res.Orders), len(res.Skipped), res.Skipped)
	}
}

// Rule 2: only the first occurrence of an order_id is processed, even when
// it is invalid; later ones are skipped.
func TestCleanDuplicates(t *testing.T) {
	first := validRaw(2, "ORD-1")
	first.Quantity = "-1" // first occurrence invalid
	rows := []RawRow{
		first,
		validRaw(3, "ORD-1"),   // duplicate of invalid row: still skipped
		validRaw(4, "ORD-2"),   // valid
		validRaw(5, " ORD-2 "), // duplicate after trimming
		validRaw(6, "ORD-2"),   // second duplicate
	}
	res := Clean(rows)
	if len(res.Orders) != 1 || res.Orders[0].Line != 4 {
		t.Fatalf("orders = %+v, want only line 4", res.Orders)
	}
	want := []string{ReasonBadQuantity, ReasonDuplicate, ReasonDuplicate, ReasonDuplicate}
	got := reasons(res.Skipped)
	if len(got) != len(want) {
		t.Fatalf("skipped reasons = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("skipped[%d] = %s, want %s", i, got[i], want[i])
		}
	}
	if res.Skipped[1].Line != 3 || res.Skipped[1].OrderID != "ORD-1" {
		t.Errorf("duplicate log entry = %+v, want line 3 ORD-1", res.Skipped[1])
	}
}

func TestCleanSkipReasons(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*RawRow)
		reason string
	}{
		{"bad date", func(r *RawRow) { r.OrderDate = "когда-то" }, ReasonBadDate},
		{"empty date", func(r *RawRow) { r.OrderDate = "" }, ReasonBadDate},
		{"empty quantity", func(r *RawRow) { r.Quantity = "" }, ReasonBadQuantity},
		{"text quantity", func(r *RawRow) { r.Quantity = "n/a" }, ReasonBadQuantity},
		{"zero quantity", func(r *RawRow) { r.Quantity = "0" }, ReasonBadQuantity},
		{"negative quantity", func(r *RawRow) { r.Quantity = "-3" }, ReasonBadQuantity},
		{"text price", func(r *RawRow) { r.UnitPrice = "договорная" }, ReasonBadPrice},
		{"empty price", func(r *RawRow) { r.UnitPrice = "" }, ReasonBadPrice},
		{"bad discount", func(r *RawRow) { r.Discount = "много" }, ReasonBadDiscount},
		{"missing id", func(r *RawRow) { r.OrderID = " " }, ReasonMissingOrderID},
		{"missing region", func(r *RawRow) { r.Region = "  " }, ReasonMissingRegion},
		{"missing category", func(r *RawRow) { r.Category = "" }, ReasonMissingCategory},
		{"missing product", func(r *RawRow) { r.Product = "" }, ReasonMissingProduct},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			r := validRaw(7, "X")
			tc.mutate(&r)
			res := Clean([]RawRow{r})
			if len(res.Orders) != 0 || len(res.Skipped) != 1 {
				t.Fatalf("got %d valid, %d skipped", len(res.Orders), len(res.Skipped))
			}
			s := res.Skipped[0]
			if s.Reason != tc.reason || s.Line != 7 || s.Detail == "" {
				t.Errorf("skipped = %+v, want reason %s on line 7 with detail", s, tc.reason)
			}
		})
	}
}

func TestCleanNormalizesValidRow(t *testing.T) {
	r := validRaw(2, " ORD-9 ")
	r.Region = "  Казань "
	r.OrderDate = "05.03.2025"
	r.UnitPrice = "1234,50"
	r.Discount = "15%"
	r.Quantity = "4"
	res := Clean([]RawRow{r})
	if len(res.Orders) != 1 {
		t.Fatalf("expected a valid order, skipped: %+v", res.Skipped)
	}
	o := res.Orders[0]
	if o.OrderID != "ORD-9" || o.Region != "Казань" || o.Month() != "2025-03" ||
		o.Quantity != 4 || o.UnitPrice != 1234.5 || o.Discount != 0.15 {
		t.Errorf("unexpected order %+v", o)
	}
	// Rule 8: revenue = quantity * unit_price * (1 - discount).
	if got, want := o.Revenue(), 4*1234.5*0.85; got != want {
		t.Errorf("Revenue = %v, want %v", got, want)
	}
}

func TestCleanEmptyDiscountIsZero(t *testing.T) {
	r := validRaw(2, "A")
	r.Discount = ""
	res := Clean([]RawRow{r})
	if len(res.Orders) != 1 || res.Orders[0].Discount != 0 || res.Orders[0].Revenue() != 200 {
		t.Fatalf("unexpected result %+v", res)
	}
}
