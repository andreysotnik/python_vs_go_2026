// Package charts renders the report charts as PNG files using gonum/plot.
package charts

import (
	"fmt"
	"image/color"
	"math"
	"os"
	"path/filepath"
	"slices"

	xfont "golang.org/x/image/font"
	"gonum.org/v1/plot"
	"gonum.org/v1/plot/font"
	"gonum.org/v1/plot/plotter"
	"gonum.org/v1/plot/vg"
	"gonum.org/v1/plot/vg/draw"
	"gonum.org/v1/plot/vg/vgimg"

	"salesreport/internal/numfmt"
	"salesreport/internal/sales"
)

// Output file names.
const (
	FileRevenueByMonth  = "revenue_by_month.png"
	FileRevenueByRegion = "revenue_by_region.png"
	FileCategoryShare   = "category_share.png"
	FileTopProducts     = "top_products.png"
	FilePlanVsFact      = "plan_vs_fact.png"
)

// Files lists all chart files produced by RenderAll, in report order.
var Files = []string{
	FileRevenueByMonth, FileRevenueByRegion, FileCategoryShare, FileTopProducts, FilePlanVsFact,
}

const (
	width  = 11 * vg.Inch
	height = 6 * vg.Inch
	dpi    = 150
)

var (
	colorPrimary = color.RGBA{R: 0x2b, G: 0x6c, B: 0xb0, A: 0xff}
	colorPlan    = color.RGBA{R: 0xb8, G: 0xc4, B: 0xd6, A: 0xff}
	colorGrid    = color.RGBA{R: 0xdd, G: 0xdd, B: 0xdd, A: 0xff}
	colorText    = color.RGBA{R: 0x33, G: 0x33, B: 0x33, A: 0xff}
	// palette is a colour-blind friendly categorical palette (Okabe-Ito based).
	palette = []color.Color{
		color.RGBA{R: 0x00, G: 0x72, B: 0xb2, A: 0xff},
		color.RGBA{R: 0xe6, G: 0x9f, B: 0x00, A: 0xff},
		color.RGBA{R: 0x00, G: 0x9e, B: 0x73, A: 0xff},
		color.RGBA{R: 0xcc, G: 0x79, B: 0xa7, A: 0xff},
		color.RGBA{R: 0x56, G: 0xb4, B: 0xe9, A: 0xff},
		color.RGBA{R: 0xd5, G: 0x5e, B: 0x00, A: 0xff},
		color.RGBA{R: 0xf0, G: 0xe4, B: 0x42, A: 0xff},
		color.RGBA{R: 0x99, G: 0x99, B: 0x99, A: 0xff},
	}
)

// sansFont is used for all chart text: Liberation Sans ships with gonum/plot,
// covers Cyrillic and reads better on charts than the default serif face.
var sansFont = font.Font{Typeface: "Liberation", Variant: "Sans"}

func init() {
	// gonum/plot reads these package-level defaults when building plots and
	// labels; there is no per-plot option for the default typeface.
	plot.DefaultFont = sansFont
	plotter.DefaultFont = sansFont
}

// RenderAll writes all five charts for the summary into dir.
func RenderAll(s sales.Summary, dir string) error {
	steps := []struct {
		file   string
		render func(sales.Summary) (*plot.Plot, error)
	}{
		{FileRevenueByMonth, revenueByMonth},
		{FileRevenueByRegion, revenueByRegion},
		{FileCategoryShare, categoryShare},
		{FileTopProducts, topProducts},
		{FilePlanVsFact, planVsFact},
	}
	for _, st := range steps {
		p, err := st.render(s)
		if err != nil {
			return fmt.Errorf("build chart %s: %w", st.file, err)
		}
		if err := savePNG(p, filepath.Join(dir, st.file)); err != nil {
			return fmt.Errorf("save chart %s: %w", st.file, err)
		}
	}
	return nil
}

func savePNG(p *plot.Plot, path string) (err error) {
	c := vgimg.PngCanvas{Canvas: vgimg.NewWith(vgimg.UseWH(width, height), vgimg.UseDPI(dpi))}
	// Leave a small margin so axis labels do not touch the image border.
	m := vg.Points(12)
	p.Draw(draw.Crop(draw.New(c), m, -m, m, -m))
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer func() {
		if cerr := f.Close(); cerr != nil && err == nil {
			err = cerr
		}
	}()
	_, err = c.WriteTo(f)
	return err
}

// newPlot creates a plot with the common styling.
func newPlot(title string) *plot.Plot {
	p := plot.New()
	p.Title.Text = title
	p.Title.TextStyle.Font.Size = 18
	p.Title.TextStyle.Font.Weight = xfont.WeightBold
	p.Title.Padding = 12
	for _, a := range []*plot.Axis{&p.X, &p.Y} {
		a.Label.TextStyle.Font.Size = 13
		a.Label.TextStyle.Color = colorText
		a.Tick.Label.Font.Size = 11
		a.Tick.Label.Color = colorText
	}
	return p
}

// thousandsTicks places about four to six "nice" major ticks (steps of 1, 2, 2.5
// or 5 times a power of ten) labelled with thousand separators
// ("12 500 000"), plus one unlabelled minor tick between them.
type thousandsTicks struct{}

func (thousandsTicks) Ticks(minV, maxV float64) []plot.Tick {
	if !(maxV > minV) {
		return []plot.Tick{{Value: minV, Label: numfmt.Thousands(minV, 0)}}
	}
	step := niceStep((maxV - minV) / 6)
	var ticks []plot.Tick
	start := math.Ceil(minV/step) * step
	for v := start; v <= maxV+step*1e-9; v += step {
		ticks = append(ticks, plot.Tick{Value: v, Label: numfmt.Thousands(v, 0)})
		if mid := v + step/2; mid <= maxV {
			ticks = append(ticks, plot.Tick{Value: mid})
		}
	}
	return ticks
}

// niceStep rounds a raw step up to 1, 2, 2.5 or 5 times a power of ten.
func niceStep(raw float64) float64 {
	pow := math.Pow(10, math.Floor(math.Log10(raw)))
	for _, m := range []float64{1, 2, 2.5, 5, 10} {
		if raw <= m*pow {
			return m * pow
		}
	}
	return 10 * pow
}

func addGrid(p *plot.Plot, vertical, horizontal bool) {
	g := plotter.NewGrid()
	g.Vertical.Color = colorGrid
	g.Horizontal.Color = colorGrid
	if !vertical {
		g.Vertical.Color = nil
	}
	if !horizontal {
		g.Horizontal.Color = nil
	}
	p.Add(g)
}

// sortedKeys returns map keys in ascending order.
func sortedKeys(m map[string]float64) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	return keys
}

// valueLabels builds compact value labels ("2,9 млн") placed at xys.
func valueLabels(xys plotter.XYs, values []float64, offset vg.Point) (*plotter.Labels, error) {
	texts := make([]string, len(values))
	for i, v := range values {
		texts[i] = numfmt.Compact(v)
	}
	l, err := plotter.NewLabels(plotter.XYLabels{XYs: xys, Labels: texts})
	if err != nil {
		return nil, err
	}
	l.Offset = offset
	for i := range l.TextStyle {
		l.TextStyle[i].Font.Size = 10
		l.TextStyle[i].Color = colorText
		l.TextStyle[i].XAlign = draw.XCenter
	}
	return l, nil
}

var monthNames = []string{"янв", "фев", "мар", "апр", "май", "июн", "июл", "авг", "сен", "окт", "ноя", "дек"}

// monthLabel converts "2025-03" to "мар 2025".
func monthLabel(ym string) string {
	var y, m int
	if _, err := fmt.Sscanf(ym, "%d-%d", &y, &m); err != nil || m < 1 || m > 12 {
		return ym
	}
	return fmt.Sprintf("%s\n%d", monthNames[m-1], y)
}

func revenueByMonth(s sales.Summary) (*plot.Plot, error) {
	if len(s.RevenueByMonth) == 0 {
		return noDataPlot("Выручка по месяцам"), nil
	}
	p := newPlot("Выручка по месяцам")
	p.Y.Label.Text = "Выручка, руб."
	p.X.Label.Text = "Месяц"
	p.Y.Tick.Marker = thousandsTicks{}
	addGrid(p, false, true)

	months := sortedKeys(s.RevenueByMonth)
	xys := make(plotter.XYs, len(months))
	values := make([]float64, len(months))
	names := make([]string, len(months))
	for i, m := range months {
		xys[i] = plotter.XY{X: float64(i), Y: s.RevenueByMonth[m]}
		values[i] = s.RevenueByMonth[m]
		names[i] = monthLabel(m)
	}
	line, points, err := plotter.NewLinePoints(xys)
	if err != nil {
		return nil, err
	}
	line.Color = colorPrimary
	line.Width = vg.Points(2.5)
	points.Shape = draw.CircleGlyph{}
	points.Color = colorPrimary
	points.Radius = vg.Points(4)
	labels, err := valueLabels(xys, values, vg.Point{Y: vg.Points(12)})
	if err != nil {
		return nil, err
	}
	p.Add(line, points, labels)
	p.NominalX(names...)
	p.X.Min, p.X.Max = -0.5, float64(len(months))-0.5
	p.Y.Min = 0
	p.Y.Max = maxOf(values) * 1.12
	return p, nil
}

func revenueByRegion(s sales.Summary) (*plot.Plot, error) {
	if len(s.RevenueByRegion) == 0 {
		return noDataPlot("Выручка по регионам"), nil
	}
	p := newPlot("Выручка по регионам")
	p.Y.Label.Text = "Выручка, руб."
	p.X.Label.Text = "Регион"
	p.Y.Tick.Marker = thousandsTicks{}
	addGrid(p, false, true)

	regions := sortedKeys(s.RevenueByRegion)
	values := make(plotter.Values, len(regions))
	xys := make(plotter.XYs, len(regions))
	for i, r := range regions {
		values[i] = s.RevenueByRegion[r]
		xys[i] = plotter.XY{X: float64(i), Y: values[i]}
	}
	bars, err := plotter.NewBarChart(values, vg.Points(60))
	if err != nil {
		return nil, err
	}
	bars.Color = colorPrimary
	bars.LineStyle.Width = 0
	labels, err := valueLabels(xys, values, vg.Point{Y: vg.Points(5)})
	if err != nil {
		return nil, err
	}
	p.Add(bars, labels)
	p.NominalX(regions...)
	padNominalX(p, len(regions))
	p.Y.Max = maxOf(values) * 1.1
	return p, nil
}

func topProducts(s sales.Summary) (*plot.Plot, error) {
	if len(s.TopProducts) == 0 {
		return noDataPlot("Топ товаров по выручке"), nil
	}
	p := newPlot(fmt.Sprintf("Топ-%d товаров по выручке", len(s.TopProducts)))
	p.X.Label.Text = "Выручка, руб."
	p.X.Tick.Marker = thousandsTicks{}
	addGrid(p, true, false)

	// Highest revenue on top: the Y axis grows upwards, so reverse.
	n := len(s.TopProducts)
	values := make(plotter.Values, n)
	names := make([]string, n)
	xys := make(plotter.XYs, n)
	for i, ps := range s.TopProducts {
		j := n - 1 - i
		values[j] = ps.Revenue
		names[j] = ps.Product
		xys[j] = plotter.XY{X: ps.Revenue, Y: float64(j)}
	}
	bars, err := plotter.NewBarChart(values, vg.Points(22))
	if err != nil {
		return nil, err
	}
	bars.Horizontal = true
	bars.Color = colorPrimary
	bars.LineStyle.Width = 0
	labels, err := valueLabels(xys, values, vg.Point{X: vg.Points(6), Y: -vg.Points(4)})
	if err != nil {
		return nil, err
	}
	for i := range labels.TextStyle {
		labels.TextStyle[i].XAlign = draw.XLeft
	}
	p.Add(bars, labels)
	p.NominalY(names...)
	p.X.Max = maxOf(values) * 1.12
	return p, nil
}

func planVsFact(s sales.Summary) (*plot.Plot, error) {
	if len(s.PlanVsFact) == 0 {
		return noDataPlot("План / факт по регионам за год"), nil
	}
	p := newPlot("План / факт по регионам за год")
	p.Y.Label.Text = "Выручка, руб."
	p.X.Label.Text = "Регион"
	p.Y.Tick.Marker = thousandsTicks{}
	addGrid(p, false, true)

	n := len(s.PlanVsFact)
	plan := make(plotter.Values, n)
	fact := make(plotter.Values, n)
	names := make([]string, n)
	factXYs := make(plotter.XYs, n)
	pctTexts := make([]string, n)
	for i, pf := range s.PlanVsFact {
		plan[i], fact[i] = pf.Target, pf.Fact
		names[i] = pf.Region
		factXYs[i] = plotter.XY{X: float64(i), Y: pf.Fact}
		pctTexts[i] = numfmt.Thousands(pf.CompletionPct, 1) + "%"
	}
	const barW = 34
	planBars, err := plotter.NewBarChart(plan, vg.Points(barW))
	if err != nil {
		return nil, err
	}
	planBars.Color = colorPlan
	planBars.LineStyle.Width = 0
	planBars.Offset = -vg.Points(barW / 2)
	factBars, err := plotter.NewBarChart(fact, vg.Points(barW))
	if err != nil {
		return nil, err
	}
	factBars.Color = colorPrimary
	factBars.LineStyle.Width = 0
	factBars.Offset = vg.Points(barW / 2)

	pct, err := plotter.NewLabels(plotter.XYLabels{XYs: factXYs, Labels: pctTexts})
	if err != nil {
		return nil, err
	}
	pct.Offset = vg.Point{X: vg.Points(barW / 2), Y: vg.Points(5)}
	for i := range pct.TextStyle {
		pct.TextStyle[i].Font.Size = 10
		pct.TextStyle[i].XAlign = draw.XCenter
		pct.TextStyle[i].Color = colorText
	}

	p.Add(planBars, factBars, pct)
	p.Legend.Add("План", planBars)
	p.Legend.Add("Факт (подпись — % выполнения)", factBars)
	p.Legend.Top = true
	p.Legend.Left = true
	p.Legend.XOffs = vg.Points(12)
	p.Legend.TextStyle.Font.Size = 12
	p.NominalX(names...)
	padNominalX(p, n)
	p.Y.Max = math.Max(maxOf(plan), maxOf(fact)) * 1.18
	return p, nil
}

// noDataPlot is a placeholder chart used when there is nothing to show.
func noDataPlot(title string) *plot.Plot {
	p := newPlot(title)
	p.HideAxes()
	p.Add(message{Text: "Нет данных"})
	return p
}

// message draws a centred text in the data area.
type message struct{ Text string }

func (m message) Plot(c draw.Canvas, p *plot.Plot) {
	sty := p.Legend.TextStyle
	sty.Font.Size = 20
	sty.Color = colorText
	sty.XAlign, sty.YAlign = draw.XCenter, draw.YCenter
	c.FillText(sty, c.Center(), m.Text)
}

// padNominalX adds horizontal room around n nominal categories so the
// outermost bars and labels are not clipped.
func padNominalX(p *plot.Plot, n int) {
	p.X.Min, p.X.Max = -0.6, float64(n)-0.4
}

func maxOf(vs []float64) float64 {
	m := 0.0
	for _, v := range vs {
		m = math.Max(m, v)
	}
	if m == 0 {
		return 1
	}
	return m
}
