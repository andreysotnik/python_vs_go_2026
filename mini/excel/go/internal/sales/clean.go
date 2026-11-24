package sales

import "fmt"

// Skip reasons. They are stable identifiers written to skipped.csv.
const (
	ReasonDuplicate       = "duplicate_order_id"
	ReasonMissingOrderID  = "missing_order_id"
	ReasonBadDate         = "invalid_order_date"
	ReasonBadQuantity     = "invalid_quantity"
	ReasonBadPrice        = "invalid_unit_price"
	ReasonBadDiscount     = "invalid_discount"
	ReasonMissingRegion   = "missing_region"
	ReasonMissingCategory = "missing_category"
	ReasonMissingProduct  = "missing_product"
)

// CleanResult is the outcome of Clean.
type CleanResult struct {
	Orders  []Order
	Skipped []Skipped
}

// Clean applies the cleaning rules to raw rows in sheet order:
//
//   - fully empty rows are ignored (neither valid nor skipped);
//   - for a duplicated order_id only the first occurrence is processed
//     (even if it turns out invalid), later ones are skipped;
//   - each remaining row is validated field by field and skipped with the
//     first failing reason.
func Clean(rows []RawRow) CleanResult {
	var res CleanResult
	seen := make(map[string]struct{}, len(rows))
	for _, r := range rows {
		if r.IsEmpty() {
			continue
		}
		id := trimSpace(r.OrderID)
		if id != "" {
			if _, dup := seen[id]; dup {
				res.Skipped = append(res.Skipped, Skipped{
					Line: r.Line, OrderID: id, Reason: ReasonDuplicate,
					Detail: "order_id already seen in an earlier row",
				})
				continue
			}
			seen[id] = struct{}{}
		}
		o, reason, err := validate(r)
		if err != nil {
			res.Skipped = append(res.Skipped, Skipped{
				Line: r.Line, OrderID: id, Reason: reason, Detail: err.Error(),
			})
			continue
		}
		res.Orders = append(res.Orders, o)
	}
	return res
}

// validate converts a raw row into an Order. On failure it returns the
// reason code and a descriptive error.
func validate(r RawRow) (Order, string, error) {
	o := Order{
		Line:     r.Line,
		OrderID:  trimSpace(r.OrderID),
		Region:   NormalizeRegion(r.Region),
		Category: trimSpace(r.Category),
		Product:  trimSpace(r.Product),
		Manager:  trimSpace(r.Manager),
	}
	if o.OrderID == "" {
		return Order{}, ReasonMissingOrderID, fmt.Errorf("order_id is empty")
	}
	var err error
	if o.Date, err = ParseDate(r.OrderDate); err != nil {
		return Order{}, ReasonBadDate, err
	}
	if o.Quantity, err = ParseQuantity(r.Quantity); err != nil {
		return Order{}, ReasonBadQuantity, err
	}
	if o.UnitPrice, err = ParsePrice(r.UnitPrice); err != nil {
		return Order{}, ReasonBadPrice, err
	}
	if o.Discount, err = ParseDiscount(r.Discount); err != nil {
		return Order{}, ReasonBadDiscount, err
	}
	switch {
	case o.Region == "":
		return Order{}, ReasonMissingRegion, fmt.Errorf("region is empty")
	case o.Category == "":
		return Order{}, ReasonMissingCategory, fmt.Errorf("category is empty")
	case o.Product == "":
		return Order{}, ReasonMissingProduct, fmt.Errorf("product is empty")
	}
	return o, "", nil
}
