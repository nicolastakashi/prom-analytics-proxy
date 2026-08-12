package leaderelection

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

// metrics is generic on the elector wrapper, not per-strategy: both the
// advisory-lock strategy and (later) the lease strategy flow through the
// same Run retry-loop, so instrumenting it once here covers both for free.
type metrics struct {
	isLeader    *prometheus.GaugeVec
	transitions *prometheus.CounterVec
}

func newMetrics(reg prometheus.Registerer) *metrics {
	if reg == nil {
		reg = prometheus.DefaultRegisterer
	}
	return &metrics{
		isLeader: promauto.With(reg).NewGaugeVec(prometheus.GaugeOpts{
			Name: "leaderelection_is_leader",
			Help: "1 if this process currently holds leadership for lease_name, 0 otherwise.",
		}, []string{"lease_name"}),
		transitions: promauto.With(reg).NewCounterVec(prometheus.CounterOpts{
			Name: "leaderelection_transitions_total",
			Help: "Total number of leadership transitions, labeled by the direction of the transition (to=\"leader\" or to=\"follower\").",
		}, []string{"lease_name", "to"}),
	}
}
