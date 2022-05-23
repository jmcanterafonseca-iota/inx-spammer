package prometheus

import (
	"github.com/prometheus/client_golang/prometheus"
)

var (
	sentSpamBlocks prometheus.Gauge
)

func configureSpammerMetrics(registry *prometheus.Registry) {

	sentSpamBlocks = prometheus.NewGauge(
		prometheus.GaugeOpts{
			Namespace: "iota",
			Subsystem: "spammer",
			Name:      "sent_spam_blocks",
			Help:      "Sent spam blocks by the spammer plugin.",
		})
	registry.MustRegister(sentSpamBlocks)
}

func collectSpammerMetrics() {
	if sentSpamBlocks != nil {
		sentSpamBlocks.Set(float64(deps.SpammerMetrics.SentSpamBlocks.Load()))
	}
}
