package db

import (
	"context"
	"fmt"
	"testing"
	"time"
)

func makeBenchQueries(n int) []Query {
	now := time.Now().UTC()
	qs := make([]Query, 0, n)
	for i := 0; i < n; i++ {
		qs = append(qs, Query{
			TS:                    now.Add(time.Duration(i) * time.Millisecond),
			QueryParam:            fmt.Sprintf("rate(http_requests_total{job=\"svc-%d\"}[5m])", i%50),
			TimeParam:             now,
			Duration:              10 * time.Millisecond,
			StatusCode:            200,
			BodySize:              512,
			LabelMatchers:         LabelMatchers{{"__name__": "http_requests_total"}},
			Fingerprint:           fmt.Sprintf("fp-%d", i),
			Type:                  QueryTypeRange,
			Step:                  15,
			Start:                 now.Add(-time.Hour),
			End:                   now,
			TotalQueryableSamples: 1000,
			PeakSamples:           2000,
			HTTPHeaders:           map[string]string{"X-Bench": "1"},
		})
	}
	return qs
}

// BenchmarkPostgreSQLInsert measures PostGreSQLProvider.Insert (pq.CopyIn
// bulk load) wall-clock cost for batch sizes matching the acceptance
// criteria - 100 and 1000 rows.
func BenchmarkPostgreSQLInsert(b *testing.B) {
	prov := newTestPostgreSQLProvider(b)

	for _, n := range []int{100, 1000} {
		b.Run(fmt.Sprintf("batch%d", n), func(b *testing.B) {
			queries := makeBenchQueries(n)
			b.ResetTimer()

			for b.Loop() {
				if err := prov.Insert(context.Background(), queries); err != nil {
					b.Fatalf("Insert: %v", err)
				}
			}
		})
	}
}
