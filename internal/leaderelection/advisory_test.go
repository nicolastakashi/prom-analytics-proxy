package leaderelection

import (
	"context"
	"testing"
	"testing/synctest"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/stretchr/testify/assert"
)

// TestAdvisoryElector_ReturnsWhenContextAlreadyCanceled pins the invariant
// that Run checks ctx before ever touching db — a nil *sql.DB is safe here
// precisely because that check must happen first.
func TestAdvisoryElector_ReturnsWhenContextAlreadyCanceled(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		elector := NewAdvisoryElector(nil, prometheus.NewRegistry())

		ctx, cancel := context.WithCancel(context.Background())
		cancel()

		done := make(chan error, 1)
		go func() { done <- elector.Run(ctx, "already-canceled-test", func(context.Context, CycleReporter) {}) }()

		assert.NoError(t, mustReceive(t, done, "Run must return without waiting for an already-canceled ctx"))
	})
}
