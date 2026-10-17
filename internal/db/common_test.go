package db

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

// TestScanRows_RowThatFailsToDecodeFailsTheRead verifies a row the scanner
// cannot decode fails the whole read rather than being skipped. Only SQLite
// can store labels that are not valid JSON.
func TestScanRows_RowThatFailsToDecodeFailsTheRead(t *testing.T) {
	t.Parallel()
	p := newTestSQLiteProvider(t)

	now := time.Now().UTC()
	sqliteTestBackend.mustExec(t, p, `INSERT INTO RulesUsage (serie, group_name, name, expression, kind, labels, created_at, first_seen_at, last_seen_at)
		VALUES ('up', 'g', 'r', 'up', 'alert', 'not json', ?, ?, ?)`, now, now, now)

	_, err := p.GetRulesUsage(context.Background(), RulesUsageParams{
		Serie: "up", Kind: "alert", TimeRange: presenceWindow(t, now.Add(-time.Hour)), Page: 1, PageSize: 10,
	})
	var syntaxErr *json.SyntaxError
	assert.ErrorAs(t, err, &syntaxErr)
}
