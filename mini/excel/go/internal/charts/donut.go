package charts

import (
	"cmp"
	"image/color"
	"math"
	"slices"

	xfont "golang.org/x/image/font"
	"gonum.org/v1/plot"
	"gonum.org/v1/plot/text"
	"gonum.org/v1/plot/vg"
	"gonum.org/v1/plot/vg/draw"

	"salesreport/internal/numfmt"
	"salesreport/internal/sales"
)

// segment is one sector of the donut chart.
type segment struct {
	Name  string
	Value float64
	Color color.Color
}

// donut is a plot.Plotter drawing a ring chart with a legend on the right.
// gonum/plot has no pie chart, so it is drawn directly on the canvas.
type donut struct {
	Slices    []segment
	Total     float64
	TextStyle text.Style
}

const arcSteps = 180 // polygon points per full circle

func (d *donut) Plot(c draw.Canvas, _ *plot.Plot) {
	if d.Total <= 0 {
		return
	}
	w := c.Max.X - c.Min.X
	h := c.Max.Y - c.Min.Y
	outer := min(w*0.45, h) / 2 * 0.95
	inner := outer * 0.55
	center := vg.Point{X: c.Min.X + w*0.27, Y: c.Min.Y + h/2}

	style := func(size vg.Length, bold bool, clr color.Color, xa draw.XAlignment) draw.TextStyle {
		s := d.TextStyle
		s.Font.Size = size
		if bold {
			s.Font.Weight = xfont.WeightBold
		}
		s.Color = clr
		s.XAlign = xa
		s.YAlign = draw.YCenter
		return s
	}

	angle := math.Pi / 2 // start at 12 o'clock, go clockwise
	for _, sl := range d.Slices {
		sweep := sl.Value / d.Total * 2 * math.Pi
		c.FillPolygon(sl.Color, ringSegment(center, inner, outer, angle, angle-sweep))
		share := sl.Value / d.Total
		if share >= 0.03 {
			mid := angle - sweep/2
			r := (inner + outer) / 2
			pt := vg.Point{X: center.X + r*vg.Length(math.Cos(mid)), Y: center.Y + r*vg.Length(math.Sin(mid))}
			c.FillText(style(12, true, color.White, draw.XCenter), pt, numfmt.Thousands(share*100, 1)+"%")
		}
		angle -= sweep
	}

	c.FillText(style(12, false, colorText, draw.XCenter), vg.Point{X: center.X, Y: center.Y + 10}, "Итого, руб.")
	c.FillText(style(13, true, colorText, draw.XCenter), vg.Point{X: center.X, Y: center.Y - 10}, numfmt.Thousands(d.Total, 0))

	// Legend.
	lx := c.Min.X + w*0.56
	rowH := vg.Points(30)
	top := center.Y + rowH*vg.Length(len(d.Slices)-1)/2
	box := vg.Points(14)
	for i, sl := range d.Slices {
		y := top - rowH*vg.Length(i)
		c.FillPolygon(sl.Color, []vg.Point{
			{X: lx, Y: y - box/2}, {X: lx + box, Y: y - box/2},
			{X: lx + box, Y: y + box/2}, {X: lx, Y: y + box/2},
		})
		label := sl.Name + " — " + numfmt.Thousands(sl.Value/d.Total*100, 1) + "% (" +
			numfmt.Thousands(sl.Value, 0) + " руб.)"
		c.FillText(style(13, false, colorText, draw.XLeft), vg.Point{X: lx + box + 8, Y: y}, label)
	}
}

// ringSegment returns the polygon of a ring sector between angles a0 and a1
// (radians, a1 < a0 for clockwise drawing).
func ringSegment(center vg.Point, inner, outer vg.Length, a0, a1 float64) []vg.Point {
	steps := max(2, int(math.Ceil(math.Abs(a0-a1)/(2*math.Pi)*arcSteps)))
	pts := make([]vg.Point, 0, 2*(steps+1))
	at := func(r vg.Length, a float64) vg.Point {
		return vg.Point{X: center.X + r*vg.Length(math.Cos(a)), Y: center.Y + r*vg.Length(math.Sin(a))}
	}
	for i := 0; i <= steps; i++ {
		pts = append(pts, at(outer, a0+(a1-a0)*float64(i)/float64(steps)))
	}
	for i := steps; i >= 0; i-- {
		pts = append(pts, at(inner, a0+(a1-a0)*float64(i)/float64(steps)))
	}
	return pts
}

func categoryShare(s sales.Summary) (*plot.Plot, error) {
	const title = "Доли категорий в выручке"
	if len(s.RevenueByCategory) == 0 {
		return noDataPlot(title), nil
	}
	p := newPlot(title)
	p.HideAxes()

	segments := make([]segment, 0, len(s.RevenueByCategory))
	total := 0.0
	for name, v := range s.RevenueByCategory {
		segments = append(segments, segment{Name: name, Value: v})
		total += v
	}
	slices.SortFunc(segments, func(a, b segment) int {
		if c := cmp.Compare(b.Value, a.Value); c != 0 {
			return c
		}
		return cmp.Compare(a.Name, b.Name)
	})
	for i := range segments {
		segments[i].Color = palette[i%len(palette)]
	}
	p.Add(&donut{Slices: segments, Total: total, TextStyle: p.Legend.TextStyle})
	return p, nil
}
