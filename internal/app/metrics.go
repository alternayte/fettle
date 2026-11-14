package app

import (
	"context"

	"github.com/alternayte/drel"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
)

// Metrics holds the Prometheus families of §17.
type Metrics struct {
	Registry       *prometheus.Registry
	ModelTokens    *prometheus.CounterVec
	ModelCost      *prometheus.CounterVec
	ModelCalls     *prometheus.CounterVec
	RunsFinished   *prometheus.CounterVec
	Cards          *prometheus.CounterVec
	ComputerStarts *prometheus.CounterVec
	ComputerActive *prometheus.CounterVec
	PushDeliveries *prometheus.CounterVec
}

// runsCollector reports runs by state and step from the database at scrape time.
type runsCollector struct {
	e    *drel.Engine
	desc *prometheus.Desc
}

func (c *runsCollector) Describe(ch chan<- *prometheus.Desc) { ch <- c.desc }

func (c *runsCollector) Collect(ch chan<- prometheus.Metric) {
	rows, err := c.e.Query(context.Background(), `SELECT r.status, COALESCE(e.step, 'single'), COUNT(*) FROM runs r JOIN envelopes e ON e.id = r.envelope_id GROUP BY r.status, COALESCE(e.step, 'single')`)
	if err != nil {
		return
	}
	defer rows.Close()
	for rows.Next() {
		var state, step string
		var n float64
		if rows.Scan(&state, &step, &n) == nil {
			ch <- prometheus.MustNewConstMetric(c.desc, prometheus.GaugeValue, n, state, step)
		}
	}
}

// NewMetrics registers the metric families.
func NewMetrics(e *drel.Engine) *Metrics {
	r := prometheus.NewRegistry()
	r.MustRegister(collectors.NewGoCollector(), collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}))
	m := &Metrics{
		Registry:       r,
		ModelTokens:    prometheus.NewCounterVec(prometheus.CounterOpts{Name: "fettle_model_tokens_total", Help: "Model tokens by role, model and direction."}, []string{"role", "model", "direction"}),
		ModelCost:      prometheus.NewCounterVec(prometheus.CounterOpts{Name: "fettle_model_cost_usd_total", Help: "Model cost in USD by role and model."}, []string{"role", "model"}),
		ModelCalls:     prometheus.NewCounterVec(prometheus.CounterOpts{Name: "fettle_model_calls_total", Help: "Model calls by role and model."}, []string{"role", "model"}),
		RunsFinished:   prometheus.NewCounterVec(prometheus.CounterOpts{Name: "fettle_runs_finished_total", Help: "Finished runs by state and step."}, []string{"state", "step"}),
		Cards:          prometheus.NewCounterVec(prometheus.CounterOpts{Name: "fettle_cards_total", Help: "Cards posted by kind."}, []string{"kind"}),
		ComputerStarts: prometheus.NewCounterVec(prometheus.CounterOpts{Name: "fettle_computer_starts_total", Help: "Computer starts by provider."}, []string{"provider"}),
		ComputerActive: prometheus.NewCounterVec(prometheus.CounterOpts{Name: "fettle_computer_active_seconds_total", Help: "Computer active seconds by provider and size."}, []string{"provider", "size"}),
		PushDeliveries: prometheus.NewCounterVec(prometheus.CounterOpts{Name: "fettle_push_deliveries_total", Help: "Web Push deliveries by result."}, []string{"result"}),
	}
	r.MustRegister(m.ModelTokens, m.ModelCost, m.ModelCalls, m.RunsFinished, m.Cards, m.ComputerStarts, m.ComputerActive, m.PushDeliveries)
	r.MustRegister(&runsCollector{e: e, desc: prometheus.NewDesc("fettle_runs", "Runs by state and step.", []string{"state", "step"}, nil)})
	// Families exist from the start, so a scrape lists them before the first event.
	m.ComputerStarts.WithLabelValues("none")
	m.ComputerActive.WithLabelValues("none", "none")
	m.PushDeliveries.WithLabelValues("none")
	m.Cards.WithLabelValues("none")
	m.ModelTokens.WithLabelValues("none", "none", "in")
	m.ModelCost.WithLabelValues("none", "none")
	m.ModelCalls.WithLabelValues("none", "none")
	m.RunsFinished.WithLabelValues("none", "none")
	return m
}

// RunFinished implements agent.Metrics.
func (m *Metrics) RunFinished(status, step string) {
	m.RunsFinished.WithLabelValues(status, step).Inc()
}

// ModelCall implements agent.Metrics.
func (m *Metrics) ModelCall(role, model string, in, out int64, cost float64) {
	m.ModelCalls.WithLabelValues(role, model).Inc()
	m.ModelTokens.WithLabelValues(role, model, "in").Add(float64(in))
	m.ModelTokens.WithLabelValues(role, model, "out").Add(float64(out))
	m.ModelCost.WithLabelValues(role, model).Add(cost)
}

// Card implements agent.Metrics.
func (m *Metrics) Card(kind string) { m.Cards.WithLabelValues(kind).Inc() }
