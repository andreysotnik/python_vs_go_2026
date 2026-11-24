// Package app wires reading, cleaning, aggregation and output together.
package app

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"

	"salesreport/internal/charts"
	"salesreport/internal/report"
	"salesreport/internal/sales"
	"salesreport/internal/source"
)

// ErrUsage is returned for invalid command line arguments.
var ErrUsage = errors.New("usage error")

// Config holds the command line options.
type Config struct {
	Input   string
	OutDir  string
	Verbose bool
}

// ParseArgs parses command line arguments (without the program name).
func ParseArgs(args []string, stderr io.Writer) (Config, error) {
	var cfg Config
	fs := flag.NewFlagSet("salesreport", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.StringVar(&cfg.Input, "input", "", "path to the sales .xlsx file (required)")
	fs.StringVar(&cfg.OutDir, "out", "out", "output directory")
	fs.BoolVar(&cfg.Verbose, "v", false, "log every skipped row to stderr")
	fs.Usage = func() {
		_, _ = fmt.Fprintln(stderr, "Usage: salesreport --input data/sales.xlsx --out out/ [-v]")
		fs.PrintDefaults()
	}
	if err := fs.Parse(args); err != nil {
		return cfg, fmt.Errorf("%w: %w", ErrUsage, err)
	}
	if fs.NArg() > 0 {
		return cfg, fmt.Errorf("%w: unexpected arguments: %v", ErrUsage, fs.Args())
	}
	if cfg.Input == "" {
		fs.Usage()
		return cfg, fmt.Errorf("%w: --input is required", ErrUsage)
	}
	if cfg.OutDir == "" {
		return cfg, fmt.Errorf("%w: --out must not be empty", ErrUsage)
	}
	return cfg, nil
}

// Run executes the whole pipeline and returns the computed summary.
func Run(cfg Config, log *slog.Logger) (sales.Summary, error) {
	wb, err := source.ReadWorkbook(cfg.Input)
	if err != nil {
		return sales.Summary{}, err
	}
	log.Info("workbook loaded", "file", cfg.Input, "sales_rows", len(wb.Sales), "targets", len(wb.Targets))

	cleaned := sales.Clean(wb.Sales)
	for _, s := range cleaned.Skipped {
		lvl := slog.LevelDebug
		if cfg.Verbose {
			lvl = slog.LevelWarn
		}
		log.Log(context.Background(), lvl, "row skipped",
			"row", s.Line, "order_id", s.OrderID, "reason", s.Reason, "detail", s.Detail)
	}

	summary := sales.Summarize(cleaned.Orders, len(cleaned.Skipped), wb.Targets)

	if err := os.MkdirAll(cfg.OutDir, 0o755); err != nil {
		return summary, fmt.Errorf("create output dir: %w", err)
	}
	if err := report.WriteSummary(filepath.Join(cfg.OutDir, report.FileSummary), summary); err != nil {
		return summary, err
	}
	if err := report.WriteSkipped(filepath.Join(cfg.OutDir, report.FileSkipped), cleaned.Skipped); err != nil {
		return summary, err
	}
	if err := charts.RenderAll(summary, cfg.OutDir); err != nil {
		return summary, err
	}
	specs := []report.ChartSpec{
		{Title: "Выручка по месяцам", File: charts.FileRevenueByMonth},
		{Title: "Выручка по регионам", File: charts.FileRevenueByRegion},
		{Title: "Доли категорий", File: charts.FileCategoryShare},
		{Title: "Топ-10 товаров", File: charts.FileTopProducts},
		{Title: "План / факт по регионам", File: charts.FilePlanVsFact},
	}
	if err := report.WriteHTML(cfg.OutDir, filepath.Base(cfg.Input), summary, cleaned.Skipped, specs); err != nil {
		return summary, err
	}

	log.Info("report written",
		"out", cfg.OutDir,
		"valid_rows", summary.ValidRows,
		"skipped_rows", summary.SkippedRows,
		"skipped_log", filepath.Join(cfg.OutDir, report.FileSkipped),
		"total_revenue", fmt.Sprintf("%.2f", summary.TotalRevenue))
	return summary, nil
}
