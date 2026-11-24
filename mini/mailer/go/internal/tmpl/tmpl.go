// Package tmpl renders the {{date}}, {{datetime}} and {{job}} placeholders.
package tmpl

import (
	"regexp"
	"time"
)

var placeholder = regexp.MustCompile(`\{\{\s*(date|datetime|job)\s*\}\}`)

// Formats used for placeholders.
const (
	DateFormat     = "2006-01-02"
	DateTimeFormat = "2006-01-02 15:04 -07:00"
)

// Render substitutes placeholders in s. The slot time is converted to loc
// first, so {{date}} is the calendar date in the job's time zone.
// Unknown placeholders are left untouched.
func Render(s, job string, slot time.Time, loc *time.Location) string {
	if loc == nil {
		loc = time.UTC
	}
	t := slot.In(loc)
	return placeholder.ReplaceAllStringFunc(s, func(m string) string {
		switch placeholder.FindStringSubmatch(m)[1] {
		case "date":
			return t.Format(DateFormat)
		case "datetime":
			return t.Format(DateTimeFormat)
		default:
			return job
		}
	})
}
