// Package source reads the sales workbook (sheets "Sales" and "Targets").
package source

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
	"unicode"

	"github.com/xuri/excelize/v2"

	"salesreport/internal/sales"
)

// Sheet names expected in the workbook.
const (
	SalesSheet   = "Sales"
	TargetsSheet = "Targets"
)

var salesColumns = []string{
	"order_id", "order_date", "region", "category", "product",
	"manager", "quantity", "unit_price", "discount",
}

var targetColumns = []string{"region", "month", "target"}

var monthRe = regexp.MustCompile(`^\d{4}-(0[1-9]|1[0-2])$`)

// Workbook is the raw content of the input file.
type Workbook struct {
	Sales   []sales.RawRow
	Targets []sales.Target
}

// ReadWorkbook opens an .xlsx file and reads both sheets.
func ReadWorkbook(path string) (*Workbook, error) {
	f, err := excelize.OpenFile(path)
	if err != nil {
		return nil, fmt.Errorf("open workbook %q: %w", path, err)
	}
	defer func() { _ = f.Close() }()

	salesRows, err := readSheet(f, SalesSheet)
	if err != nil {
		return nil, err
	}
	rawSales, err := parseSales(salesRows)
	if err != nil {
		return nil, fmt.Errorf("sheet %q: %w", SalesSheet, err)
	}

	targetRows, err := readSheet(f, TargetsSheet)
	if err != nil {
		return nil, err
	}
	targets, err := parseTargets(targetRows)
	if err != nil {
		return nil, fmt.Errorf("sheet %q: %w", TargetsSheet, err)
	}
	return &Workbook{Sales: rawSales, Targets: targets}, nil
}

func readSheet(f *excelize.File, name string) ([][]string, error) {
	if idx, err := f.GetSheetIndex(name); err != nil || idx < 0 {
		return nil, fmt.Errorf("sheet %q not found (available: %s)", name, strings.Join(f.GetSheetList(), ", "))
	}
	// Raw values: dates come as Excel serials, numbers without formatting.
	rows, err := f.GetRows(name, excelize.Options{RawCellValue: true})
	if err != nil {
		return nil, fmt.Errorf("read sheet %q: %w", name, err)
	}
	if len(rows) == 0 {
		return nil, fmt.Errorf("sheet %q is empty", name)
	}
	return rows, nil
}

// columnIndex maps required column names to their positions in the header.
func columnIndex(header []string, required []string) (map[string]int, error) {
	idx := make(map[string]int, len(header))
	for i, h := range header {
		key := strings.ToLower(strings.TrimFunc(h, unicode.IsSpace))
		if _, dup := idx[key]; !dup && key != "" {
			idx[key] = i
		}
	}
	var missing []string
	for _, c := range required {
		if _, ok := idx[c]; !ok {
			missing = append(missing, c)
		}
	}
	if len(missing) > 0 {
		return nil, fmt.Errorf("missing columns in header: %s", strings.Join(missing, ", "))
	}
	return idx, nil
}

func cell(row []string, i int) string {
	if i < len(row) {
		return row[i]
	}
	return ""
}

func parseSales(rows [][]string) ([]sales.RawRow, error) {
	idx, err := columnIndex(rows[0], salesColumns)
	if err != nil {
		return nil, err
	}
	out := make([]sales.RawRow, 0, len(rows)-1)
	for i, row := range rows[1:] {
		get := func(col string) string { return cell(row, idx[col]) }
		out = append(out, sales.RawRow{
			Line:      i + 2,
			OrderID:   get("order_id"),
			OrderDate: get("order_date"),
			Region:    get("region"),
			Category:  get("category"),
			Product:   get("product"),
			Manager:   get("manager"),
			Quantity:  get("quantity"),
			UnitPrice: get("unit_price"),
			Discount:  get("discount"),
		})
	}
	return out, nil
}

// parseTargets is strict: a malformed plan row is a data error, not
// something to silently drop, because it would distort plan_vs_fact.
func parseTargets(rows [][]string) ([]sales.Target, error) {
	idx, err := columnIndex(rows[0], targetColumns)
	if err != nil {
		return nil, err
	}
	var (
		out  []sales.Target
		errs []error
	)
	for i, row := range rows[1:] {
		line := i + 2
		region := sales.NormalizeRegion(cell(row, idx["region"]))
		month := strings.TrimSpace(cell(row, idx["month"]))
		rawTarget := strings.TrimSpace(cell(row, idx["target"]))
		if region == "" && month == "" && rawTarget == "" {
			continue
		}
		if region == "" {
			errs = append(errs, fmt.Errorf("row %d: empty region", line))
			continue
		}
		if !monthRe.MatchString(month) {
			errs = append(errs, fmt.Errorf("row %d: month %q is not YYYY-MM", line, month))
			continue
		}
		amount, err := sales.ParseAmount(rawTarget)
		if err != nil {
			errs = append(errs, fmt.Errorf("row %d: target: %w", line, err))
			continue
		}
		out = append(out, sales.Target{Region: region, Month: month, Amount: amount})
	}
	if len(errs) > 0 {
		return nil, errors.Join(errs...)
	}
	return out, nil
}
