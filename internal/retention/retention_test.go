package retention

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/nicolastakashi/prom-analytics-proxy/internal/config"
	"github.com/nicolastakashi/prom-analytics-proxy/internal/db"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/stretchr/testify/assert"
)

// fakeProvider is a minimal db.Provider stand-in. Embedding the (nil)
// interface means any method this worker doesn't use panics if called -
// which is what a test wants, rather than a silent zero value.
type fakeProvider struct {
	db.Provider

	deleteCalls   []time.Time
	deleteResults []int64
	deleteErrors  []error
	callIndex     int
}

func (f *fakeProvider) DeleteQueriesBefore(ctx context.Context, cutoff time.Time) (int64, error) {
	f.deleteCalls = append(f.deleteCalls, cutoff)
	if f.callIndex < len(f.deleteErrors) {
		err := f.deleteErrors[f.callIndex]
		result := int64(0)
		if f.callIndex < len(f.deleteResults) {
			result = f.deleteResults[f.callIndex]
		}
		f.callIndex++
		return result, err
	}
	result := int64(0)
	if f.callIndex < len(f.deleteResults) {
		result = f.deleteResults[f.callIndex]
	}
	f.callIndex++
	return result, nil
}

// baseRetentionConfig is a valid config every constructor test starts from,
// so each one only has to state the single field it's about.
func baseRetentionConfig() *config.Config {
	return &config.Config{
		Retention: config.RetentionConfig{
			Enabled:       true,
			Interval:      1 * time.Hour,
			RunTimeout:    5 * time.Minute,
			QueriesMaxAge: 30 * 24 * time.Hour,
		},
	}
}

func TestNewWorker(t *testing.T) {
	cfg := baseRetentionConfig()

	fakeProv := &fakeProvider{}

	w, err := NewWorker(fakeProv, cfg, prometheus.NewRegistry())
	assert.NoError(t, err)
	assert.NotNil(t, w)
	assert.Equal(t, cfg.Retention.RunTimeout, w.runTimeout)
	assert.Equal(t, cfg.Retention.QueriesMaxAge, w.queriesMaxAge)
}

// TestNewWorker_ConfigValidation covers the mechanical half of NewWorker's
// construction gate: every value it requires to be positive. The two cases
// whose reasoning needs explaining stay separate tests below.
func TestNewWorker_ConfigValidation(t *testing.T) {
	for _, tc := range []struct {
		name    string
		tweak   func(*config.RetentionConfig)
		wantErr string
	}{
		{
			name:    "interval zero",
			tweak:   func(c *config.RetentionConfig) { c.Interval = 0 },
			wantErr: "interval must be positive",
		},
		{
			name:    "interval negative",
			tweak:   func(c *config.RetentionConfig) { c.Interval = -time.Hour },
			wantErr: "interval must be positive",
		},
		{
			name:    "run_timeout zero",
			tweak:   func(c *config.RetentionConfig) { c.RunTimeout = 0 },
			wantErr: "run_timeout must be positive",
		},
		{
			name:    "run_timeout negative",
			tweak:   func(c *config.RetentionConfig) { c.RunTimeout = -time.Minute },
			wantErr: "run_timeout must be positive",
		},
		{
			name:    "queries_max_age zero",
			tweak:   func(c *config.RetentionConfig) { c.QueriesMaxAge = 0 },
			wantErr: "queries_max_age must be positive",
		},
		{
			name:    "queries_max_age negative",
			tweak:   func(c *config.RetentionConfig) { c.QueriesMaxAge = -time.Hour },
			wantErr: "queries_max_age must be positive",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg := baseRetentionConfig()
			tc.tweak(&cfg.Retention)

			w, err := NewWorker(&fakeProvider{}, cfg, prometheus.NewRegistry())

			assert.Error(t, err)
			assert.Nil(t, w)
			assert.Contains(t, err.Error(), tc.wantErr)
		})
	}
}

func TestNewWorker_AcceptsIntervalTooSmallToJitter(t *testing.T) {
	cfg := baseRetentionConfig()
	cfg.Retention.Interval = 4 * time.Nanosecond

	fakeProv := &fakeProvider{}

	w, err := NewWorker(fakeProv, cfg, prometheus.NewRegistry())
	// Positive is all this validation guarantees: coping with an interval
	// too small for 20% of it to be a whole nanosecond is the scheduler's
	// job, not a reason to reject the config.
	assert.NoError(t, err)
	assert.NotNil(t, w)
}

func TestNewWorker_RejectsZeroQueriesMaxAgeEvenWhenDisabled(t *testing.T) {
	cfg := baseRetentionConfig()
	cfg.Retention.Enabled = false
	cfg.Retention.QueriesMaxAge = 0

	fakeProv := &fakeProvider{}

	w, err := NewWorker(fakeProv, cfg, prometheus.NewRegistry())
	assert.Error(t, err) // Should fail because validation happens regardless of enabled status
	assert.Nil(t, w)
	assert.Contains(t, err.Error(), "queries_max_age must be positive")
}

func TestWorker_RunOnce(t *testing.T) {
	fakeProv := &fakeProvider{
		deleteResults: []int64{42},
		deleteErrors:  []error{nil},
	}

	w := &Worker{
		dbProvider:    fakeProv,
		runTimeout:    5 * time.Minute,
		queriesMaxAge: 30 * 24 * time.Hour,
		runDuration:   prometheus.NewHistogramVec(prometheus.HistogramOpts{Name: "test_duration"}, []string{"status"}),
	}

	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Minute)
	defer cancel()

	w.RunOnce(ctx)

	assert.Len(t, fakeProv.deleteCalls, 1, "DeleteQueriesBefore should be called once")

	actualCutoff := fakeProv.deleteCalls[0]
	expectedCutoff := time.Now().UTC().Add(-w.queriesMaxAge)
	diff := actualCutoff.Sub(expectedCutoff)
	if diff < 0 {
		diff = -diff
	}
	assert.Less(t, diff, 1*time.Second, "cutoff should be approximately now - queriesMaxAge")
}

func TestWorker_RunOnce_Error(t *testing.T) {
	fakeProv := &fakeProvider{
		deleteResults: []int64{0},
		deleteErrors:  []error{errors.New("database error")},
	}

	w := &Worker{
		dbProvider:    fakeProv,
		runTimeout:    5 * time.Minute,
		queriesMaxAge: 30 * 24 * time.Hour,
		runDuration:   prometheus.NewHistogramVec(prometheus.HistogramOpts{Name: "test_duration"}, []string{"status"}),
	}

	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Minute)
	defer cancel()

	w.RunOnce(ctx)

	assert.Len(t, fakeProv.deleteCalls, 1, "DeleteQueriesBefore should be called once")
}

func TestWorker_RunOnce_SkipsDeletionWhenQueriesMaxAgeIsZero(t *testing.T) {
	fakeProv := &fakeProvider{}

	w := &Worker{
		dbProvider:    fakeProv,
		runTimeout:    5 * time.Minute,
		queriesMaxAge: 0,
		runDuration:   prometheus.NewHistogramVec(prometheus.HistogramOpts{Name: "test_duration"}, []string{"status"}),
	}

	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Minute)
	defer cancel()

	w.RunOnce(ctx)

	assert.Len(t, fakeProv.deleteCalls, 0, "DeleteQueriesBefore should not be called when queriesMaxAge is zero")
}

func TestWorker_RunOnce_SkipsDeletionWhenQueriesMaxAgeIsNegative(t *testing.T) {
	fakeProv := &fakeProvider{}

	w := &Worker{
		dbProvider:    fakeProv,
		runTimeout:    5 * time.Minute,
		queriesMaxAge: -1 * time.Hour,
		runDuration:   prometheus.NewHistogramVec(prometheus.HistogramOpts{Name: "test_duration"}, []string{"status"}),
	}

	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Minute)
	defer cancel()

	w.RunOnce(ctx)

	assert.Len(t, fakeProv.deleteCalls, 0, "DeleteQueriesBefore should not be called when queriesMaxAge is negative")
}
