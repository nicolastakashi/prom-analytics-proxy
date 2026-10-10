package db

import (
	"context"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestRunMigrations_ConcurrentProvidersAreIsolated verifies providers can be
// constructed concurrently, each migrating its own database; -race reports
// any migration state shared between them.
func TestRunMigrations_ConcurrentProvidersAreIsolated(t *testing.T) {
	t.Parallel()
	const n = 4
	var wg sync.WaitGroup
	errs := make([]error, n)
	for i := range n {
		wg.Go(func() {
			p, err := newSqliteProvider(context.Background())
			if err == nil {
				err = p.Close()
			}
			errs[i] = err
		})
	}
	wg.Wait()
	for _, err := range errs {
		require.NoError(t, err)
	}
}
