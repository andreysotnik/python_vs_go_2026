package httpapi

import (
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
)

// RegisterRuntime adds Go runtime, process and DB pool metrics.
func (m *Metrics) RegisterRuntime(pool *pgxpool.Pool) {
	m.Registry.MustRegister(collectors.NewGoCollector(), collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}))
	if pool == nil {
		return
	}
	gauge := func(name, help string, f func(s *pgxpool.Stat) float64) {
		m.Registry.MustRegister(prometheus.NewGaugeFunc(prometheus.GaugeOpts{Name: name, Help: help},
			func() float64 { return f(pool.Stat()) }))
	}
	gauge("db_pool_acquired_conns", "Connections in use.", func(s *pgxpool.Stat) float64 { return float64(s.AcquiredConns()) })
	gauge("db_pool_idle_conns", "Idle connections.", func(s *pgxpool.Stat) float64 { return float64(s.IdleConns()) })
	gauge("db_pool_total_conns", "Total connections.", func(s *pgxpool.Stat) float64 { return float64(s.TotalConns()) })
	gauge("db_pool_max_conns", "Max connections.", func(s *pgxpool.Stat) float64 { return float64(s.MaxConns()) })
	m.Registry.MustRegister(prometheus.NewCounterFunc(prometheus.CounterOpts{Name: "db_pool_acquire_total", Help: "Pool acquires."},
		func() float64 { return float64(pool.Stat().AcquireCount()) }))
	m.Registry.MustRegister(prometheus.NewCounterFunc(prometheus.CounterOpts{Name: "db_pool_empty_acquire_total", Help: "Acquires that waited for a connection."},
		func() float64 { return float64(pool.Stat().EmptyAcquireCount()) }))
}
