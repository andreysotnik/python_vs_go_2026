package source

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/xuri/excelize/v2"
)

// writeWorkbook creates an .xlsx file with the given sheets (first row is
// the header). Values are written with their Go types.
func writeWorkbook(t *testing.T, sheets map[string][][]any) string {
	t.Helper()
	f := excelize.NewFile()
	defer f.Close()
	first := true
	for name, rows := range sheets {
		if first {
			if err := f.SetSheetName("Sheet1", name); err != nil {
				t.Fatal(err)
			}
			first = false
		} else if _, err := f.NewSheet(name); err != nil {
			t.Fatal(err)
		}
		for i, row := range rows {
			cell, _ := excelize.CoordinatesToCellName(1, i+1)
			if err := f.SetSheetRow(name, cell, &row); err != nil {
				t.Fatal(err)
			}
		}
	}
	path := filepath.Join(t.TempDir(), "book.xlsx")
	if err := f.SaveAs(path); err != nil {
		t.Fatal(err)
	}
	return path
}

var targetsOK = [][]any{
	{"region", "month", "target"},
	{" Москва ", "2025-01", 1000},
	{"Москва", "2025-02", "2000,5"},
}

func TestReadWorkbook(t *testing.T) {
	path := writeWorkbook(t, map[string][][]any{
		"Sales": {
			// Columns in a different order than documented: mapping is by header.
			{"region", "order_id", "order_date", "category", "product", "manager", "quantity", "unit_price", "Discount "},
			{" Москва ", "ORD-1", 45668, "Книги", "Книга", "И.", 2, 100.5, 0.1},
			{"Казань", "ORD-2", "14.11.2025", "Спорт", "Мяч", "П.", "n/a", "1234,50"}, // short row
		},
		"Targets": targetsOK,
	})
	wb, err := ReadWorkbook(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(wb.Sales) != 2 {
		t.Fatalf("sales rows = %d", len(wb.Sales))
	}
	r := wb.Sales[0]
	if r.Line != 2 || r.OrderID != "ORD-1" || r.OrderDate != "45668" || r.Region != " Москва " ||
		r.Quantity != "2" || r.UnitPrice != "100.5" || r.Discount != "0.1" {
		t.Errorf("row 1 = %+v", r)
	}
	r = wb.Sales[1]
	if r.Line != 3 || r.OrderDate != "14.11.2025" || r.Quantity != "n/a" || r.UnitPrice != "1234,50" || r.Discount != "" {
		t.Errorf("row 2 = %+v", r)
	}
	if len(wb.Targets) != 2 || wb.Targets[0].Region != "Москва" || wb.Targets[1].Amount != 2000.5 {
		t.Errorf("targets = %+v", wb.Targets)
	}
}

func TestReadWorkbookErrors(t *testing.T) {
	salesHeader := []any{"order_id", "order_date", "region", "category", "product", "manager", "quantity", "unit_price", "discount"}
	tests := []struct {
		name   string
		sheets map[string][][]any
		want   string
	}{
		{"missing sales sheet", map[string][][]any{"Targets": targetsOK}, `sheet "Sales" not found`},
		{"missing targets sheet", map[string][][]any{"Sales": {salesHeader}}, `sheet "Targets" not found`},
		{"missing columns", map[string][][]any{
			"Sales":   {{"order_id", "region"}},
			"Targets": targetsOK,
		}, "missing columns in header: order_date, category"},
		{"bad target month", map[string][][]any{
			"Sales":   {salesHeader},
			"Targets": {{"region", "month", "target"}, {"Москва", "2025-13", 1}},
		}, `month "2025-13" is not YYYY-MM`},
		{"bad target amount", map[string][][]any{
			"Sales":   {salesHeader},
			"Targets": {{"region", "month", "target"}, {"Москва", "2025-01", "много"}},
		}, "row 2: target"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := ReadWorkbook(writeWorkbook(t, tc.sheets))
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Errorf("error = %v, want containing %q", err, tc.want)
			}
		})
	}
}

func TestReadWorkbookMissingFile(t *testing.T) {
	_, err := ReadWorkbook(filepath.Join(t.TempDir(), "nope.xlsx"))
	if err == nil || !strings.Contains(err.Error(), "open workbook") {
		t.Errorf("error = %v", err)
	}
}
