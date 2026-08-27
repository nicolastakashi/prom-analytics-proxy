package leaderelection

import (
	"context"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/stretchr/testify/assert"
)

// TestAdvisoryElector_ReturnsWhenContextAlreadyCanceled pins the invariant
// that Run checks ctx before ever touching db — a nil *sql.DB is safe here
// precisely because that check must happen first.
func TestAdvisoryElector_ReturnsWhenContextAlreadyCanceled(t *testing.T) {
	elector := NewAdvisoryElector(nil, prometheus.NewRegistry())

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	done := make(chan error, 1)
	go func() { done <- elector.Run(ctx, "already-canceled-test", func(context.Context, CycleReporter) {}) }()

	select {
	case err := <-done:
		assert.NoError(t, err)
	case <-time.After(100 * time.Millisecond):
		t.Fatal("Run should have returned promptly for an already-canceled ctx")
	}
}
