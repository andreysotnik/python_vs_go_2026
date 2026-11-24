// Command salesreport reads a sales workbook, cleans the data, computes
// aggregates and writes summary.json, charts and an HTML report.
//
// Usage:
//
//	salesreport --input data/sales.xlsx --out out/
package main

import (
	"errors"
	"log/slog"
	"os"

	"salesreport/internal/app"
)

func main() {
	os.Exit(run())
}

func run() int {
	log := slog.New(slog.NewTextHandler(os.Stderr, nil))
	cfg, err := app.ParseArgs(os.Args[1:], os.Stderr)
	if err != nil {
		if errors.Is(err, app.ErrUsage) {
			log.Error("invalid arguments", "error", err)
			return 2
		}
		log.Error("failed", "error", err)
		return 1
	}
	if _, err := app.Run(cfg, log); err != nil {
		log.Error("failed", "error", err)
		return 1
	}
	return 0
}
