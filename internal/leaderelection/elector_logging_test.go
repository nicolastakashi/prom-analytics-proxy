package leaderelection

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

// TestElector_Run_LogsErrorsFromAcquireOrHold pins the "logged" half of
// strategy's documented contract for ok=false, err!=nil: the error must
// never be returned up through Run (already covered elsewhere), but it must
// not be silently dropped either — bad credentials, an exhausted pool, or a
// broken query otherwise means the leader-elected job never runs for the
// life of the process with nothing in the logs or metrics to distinguish
// that from the normal "another replica is leader".
func TestElector_Run_LogsErrorsFromAcquireOrHold(t *testing.T) {
	handler := &recordingHandler{}
	prev := slog.Default()
	slog.SetDefault(slog.New(handler))
	t.Cleanup(func() { slog.SetDefault(prev) })

	strat := &recordingStrategy{returnErr: errors.New("simulated transient error")}
	elector := newTestElector(strat, backoffConfig{initial: 5 * time.Millisecond, max: 10 * time.Millisecond})

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	_ = elector.Run(ctx, "log-test", func(context.Context) {})

	var found bool
	for _, r := range handler.snapshot() {
		if r.Level != slog.LevelWarn || !strings.Contains(r.Message, "leader election") {
			continue
		}
		r.Attrs(func(a slog.Attr) bool {
			if a.Key == "err" {
				found = true
			}
			return true
		})
	}
	assert.True(t, found, "acquireOrHold errors must be logged (slog.Warn with the error attached), not dropped silently")
}
