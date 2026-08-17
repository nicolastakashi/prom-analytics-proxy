package leaderelection

import (
	"context"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/stretchr/testify/assert"
)

// fakeStrategy is a hand-written fake letting the metrics tests exercise
// Elector.Run without needing a real Postgres instance, since the metrics
// wiring is generic on the elector wrapper and has nothing to do with any
// particular strategy's SQL.
type fakeStrategy struct {
	acquired bool
}

func (f *fakeStrategy) acquireOrHold(ctx context.Context, name string) (context.Context, func(), bool, error) {
	if f.acquired {
		return nil, nil, false, nil
	}
	f.acquired = true
	leaderCtx, cancel := context.WithCancel(ctx)
	return leaderCtx, func() { cancel(); f.acquired = false }, true, nil
}

// TestElector_Run_UpdatesIsLeaderGaugeAndTransitionCounter proves the
// metrics are generic on the elector wrapper: leaderelection_is_leader must
// be 1 while fn runs and 0 after Run returns, and
// leaderelection_transitions_total{to="leader"} must have incremented.
func TestElector_Run_UpdatesIsLeaderGaugeAndTransitionCounter(t *testing.T) {
	elector := newTestElector(&fakeStrategy{}, backoffConfig{initial: 5 * time.Millisecond})

	fnRunning := make(chan struct{})
	ctx, cancel := context.WithCancel(context.Background())

	done := make(chan error, 1)
	go func() {
		done <- elector.Run(ctx, "metrics-test", func(fnCtx context.Context) {
			close(fnRunning)
			<-fnCtx.Done()
		})
	}()

	select {
	case <-fnRunning:
	case <-time.After(time.Second):
		t.Fatal("fn was never invoked")
	}

	assert.Equal(t, float64(1), testutil.ToFloat64(elector.m.isLeader.WithLabelValues("metrics-test")))
	assert.Equal(t, float64(1), testutil.ToFloat64(elector.m.transitions.WithLabelValues("metrics-test", "leader")))

	cancel()
	assert.NoError(t, <-done)

	assert.Equal(t, float64(0), testutil.ToFloat64(elector.m.isLeader.WithLabelValues("metrics-test")))
	assert.Equal(t, float64(1), testutil.ToFloat64(elector.m.transitions.WithLabelValues("metrics-test", "follower")))
}

// TestNewMetrics_NilRegistererDefaultsToPrometheusDefault proves the
// documented nil-reg behavior (NewAdvisoryElector's doc comment: "reg may
// be nil, in which case metrics register against
// prometheus.DefaultRegisterer") as an outcome an operator would actually
// observe — the metric shows up when the process's default metrics
// endpoint is scraped — rather than inspecting newMetrics' internals.
// promauto.With(nil) would itself resolve to DefaultRegisterer even
// without the explicit nil-check this test targets, so this also
// incidentally guards against ever removing that check as "redundant".
func TestNewMetrics_NilRegistererDefaultsToPrometheusDefault(t *testing.T) {
	m := newMetrics(nil)
	t.Cleanup(func() {
		prometheus.Unregister(m.isLeader)
		prometheus.Unregister(m.transitions)
	})
	// A GaugeVec with no label combination ever touched reports no samples
	// at all, regardless of which registry it's in — Gather() would find
	// nothing either way, which would make this test pass for the wrong
	// reason. Touching one first makes the check meaningful.
	m.isLeader.WithLabelValues("nil-registerer-test").Set(1)

	mfs, err := prometheus.DefaultGatherer.Gather()
	assert.NoError(t, err)

	var found bool
	for _, mf := range mfs {
		if mf.GetName() == "leaderelection_is_leader" {
			found = true
			break
		}
	}
	assert.True(t, found, "newMetrics(nil) must register against prometheus.DefaultRegisterer, not silently register nowhere scrapable")
}

// TestElector_Run_InitializesIsLeaderGaugeToZero_EvenIfNeverElectedLeader
// pins the fix for a fleet-wide blind spot: a GaugeVec only creates a child
// series on first WithLabelValues, so a replica that never wins an election
// would otherwise export no series for its lease at all — indistinguishable
// from "process dead" or "lease name misconfigured" when scraped. The
// registry is Gather()'d directly rather than read through
// WithLabelValues, which would create the series as a side effect of the
// assertion itself and pass even without the fix.
func TestElector_Run_InitializesIsLeaderGaugeToZero_EvenIfNeverElectedLeader(t *testing.T) {
	strat := &recordingStrategy{} // returnErr == nil: never grants leadership
	elector, reg := newTestElectorWithRegistry(strat, backoffConfig{initial: 5 * time.Millisecond, max: 5 * time.Millisecond})

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	_ = elector.Run(ctx, "zero-init-test", func(context.Context) {
		t.Fatal("fn must never run: this strategy never grants leadership")
	})

	mfs, err := reg.Gather()
	assert.NoError(t, err)

	var value *float64
	for _, mf := range mfs {
		if mf.GetName() != "leaderelection_is_leader" {
			continue
		}
		for _, m := range mf.GetMetric() {
			for _, l := range m.GetLabel() {
				if l.GetName() == "lease_name" && l.GetValue() == "zero-init-test" {
					v := m.GetGauge().GetValue()
					value = &v
				}
			}
		}
	}
	if assert.NotNil(t, value, "a follower that never won an election must still export a series for its lease, not none at all") {
		assert.Equal(t, float64(0), *value)
	}
}
