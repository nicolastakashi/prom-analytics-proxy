package leaderelection

import (
	"testing"
	"time"

	"github.com/nicolastakashi/prom-analytics-proxy/internal/config"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestStrategy_LiteralValuesAreStable pins the exact flag values operators
// already depend on (-leader-election-strategy=advisory-lock|lease).
// Every other test derives its input from these constants, so none of
// them would catch an accidental rename here — this test checks the
// literal instead.
func TestStrategy_LiteralValuesAreStable(t *testing.T) {
	assert.Equal(t, Strategy("advisory-lock"), AdvisoryLock)
	assert.Equal(t, Strategy("lease"), Lease)
}

func TestNew_RejectsUnknownStrategy(t *testing.T) {
	_, err := New(config.LeaderElectionConfig{Strategy: "bogus"}, nil, nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unknown strategy")
}

func TestNew_DefaultsToAdvisoryLockWhenStrategyEmpty(t *testing.T) {
	// A fresh registry per success-path test — New registers metrics, and
	// prometheus.DefaultRegisterer is process-global: reusing it across
	// tests that construct the same metric names would panic.
	elector, err := New(config.LeaderElectionConfig{Strategy: ""}, nil, prometheus.NewRegistry())
	assert.NoError(t, err)
	assert.NotNil(t, elector)
}

func TestNew_AdvisoryLockStrategyDoesNotRequireLeaseFields(t *testing.T) {
	elector, err := New(config.LeaderElectionConfig{Strategy: string(AdvisoryLock)}, nil, prometheus.NewRegistry())
	assert.NoError(t, err)
	assert.NotNil(t, elector)
}

// The exact phrase checked here matters: New has a third validation branch
// ("renew_interval ... must be less than lease_ttl ...") whose error text
// also contains the substring "lease_ttl" — so asserting on that alone
// would still pass even if this specific check were deleted and the input
// happened to be caught by the later branch instead. Checking the phrase
// unique to this branch is what actually proves this check fired.
func TestNew_LeaseStrategyRequiresPositiveTTL(t *testing.T) {
	_, err := New(config.LeaderElectionConfig{Strategy: string(Lease), LeaseTTL: 0, RenewInterval: time.Second}, nil, nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "requires a positive lease_ttl")
}

// Same reasoning as TestNew_LeaseStrategyRequiresPositiveTTL above: the
// third validation branch's error text also contains "renew_interval", so
// the phrase checked here must be the one unique to this specific check.
func TestNew_LeaseStrategyRequiresPositiveRenewInterval(t *testing.T) {
	_, err := New(config.LeaderElectionConfig{Strategy: string(Lease), LeaseTTL: time.Second, RenewInterval: 0}, nil, nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "requires a positive renew_interval")
}

func TestNew_LeaseStrategyRejectsRenewIntervalNotLessThanTTL(t *testing.T) {
	_, err := New(config.LeaderElectionConfig{Strategy: string(Lease), LeaseTTL: time.Second, RenewInterval: time.Second}, nil, nil)
	require.Error(t, err, "renewing no faster than the TTL guarantees eventual false expiry for a healthy holder")
	assert.Contains(t, err.Error(), "renew_interval")
}

// TestNew_LeaseStrategyConstructsSuccessfully needs no real database: like
// the advisory-lock strategy, constructing a lease strategy doesn't touch
// the database at all — only acquireOrHold does, once Run is actually
// called (see newLeaseStrategy). The leader_leases table is expected to
// already exist by then, created by internal/db's own migrations.
func TestNew_LeaseStrategyConstructsSuccessfully(t *testing.T) {
	elector, err := New(config.LeaderElectionConfig{Strategy: string(Lease), LeaseTTL: time.Second, RenewInterval: 100 * time.Millisecond}, nil, prometheus.NewRegistry())
	assert.NoError(t, err)
	assert.NotNil(t, elector)
}
