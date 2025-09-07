package main

import (
	"context"
	"fmt"
	"log/slog"
	"math/rand"
	"os"
	"runtime/debug"
	"sync"
	"time"

	"github.com/efficientgo/core/errors"
	"github.com/prometheus/client_golang/api"
	v1 "github.com/prometheus/client_golang/api/prometheus/v1"
	"github.com/prometheus/common/model"
	"github.com/prometheus/prometheus/model/labels"
	"github.com/prometheus/prometheus/promql/parser"
	"golang.org/x/exp/slices"

	"github.com/cortexproject/promqlsmith"
)

var (
	unsupportedFunctions = map[string]struct{}{
		"histogram_count":              {},
		"histogram_sum":                {},
		"histogram_fraction":           {},
		"present_over_time":            {},
		"ts_of_max_over_time":          {},
		"ts_of_min_over_time":          {},
		"ts_of_last_over_time":         {},
		"acos":                         {},
		"acosh":                        {},
		"asin":                         {},
		"asinh":                        {},
		"atan":                         {},
		"atanh":                        {},
		"cos":                          {},
		"cosh":                         {},
		"sin":                          {},
		"sinh":                         {},
		"tan":                          {},
		"tanh":                         {},
		"dag":                          {},
		"pi":                           {},
		"rad":                          {},
		"double_exponential_smoothing": {},
		"mad_over_time":                {},
	}

	enabledBinops = []parser.ItemType{
		parser.SUB,
		parser.ADD,
		parser.MUL,
		parser.MOD,
		parser.DIV,
		parser.EQLC,
		parser.NEQ,
		parser.LTE,
		parser.GTE,
		parser.LSS,
		parser.GTR,
		parser.POW,
		parser.LAND,
		parser.LOR,
		parser.LUNLESS,
	}
)

func main() {
	if err := run(); err != nil {
		slog.Error("failed to run", "error", err)
		os.Exit(1)
	}
}

func run() error {
	client, err := api.NewClient(api.Config{
		Address: "http://localhost:9091",
	})

	if err != nil {
		return errors.Wrapf(err, "create Prometheus client")
	}

	promAPI := v1.NewAPI(client)
	ctx := context.Background()
	now := time.Now()
	series, _, err := promAPI.Series(
		ctx,
		[]string{"{job!=\"\"}"},
		now.Add(-2*time.Hour), now,
	)
	if err != nil {
		return errors.Wrapf(err, "get series")
	}

	rnd := rand.New(rand.NewSource(now.Unix()))
	opts := []promqlsmith.Option{
		promqlsmith.WithEnableOffset(true),
		promqlsmith.WithEnableAtModifier(true),
		promqlsmith.WithEnabledFunctions(getAvailableFunctions()),
		promqlsmith.WithEnabledBinOps(enabledBinops),
		promqlsmith.WithEnableVectorMatching(true),
	}

	ps := promqlsmith.New(rnd, modelLabelSetToLabels(series), opts...)

	// for i := 0; i < 100; i++ {
	// 	expr := ps.WalkInstantQuery()
	// 	query := expr.Pretty(0)

	// 	_, _, err := promAPI.Query(ctx, query, now)
	// 	if err != nil {
	// 		return errors.Wrapf(err, "instant query")
	// 	}
	// }

	successCount := 0
	errorCount := 0
	panicCount := 0

	wg := sync.WaitGroup{}
	wg.Add(1)
	go func() {
		for i := 0; i < 10000; i++ {
			defer wg.Done()
			defer func() {
				if r := recover(); r != nil {
					panicCount++
					fmt.Printf("Panic recovered in iteration %d: %v\n", i, r)
					fmt.Printf("Stack trace:\n%s\n", debug.Stack())
				}
			}()

			expr := ps.WalkRangeQuery()
			query := expr.Pretty(0)

			_, _, err := promAPI.QueryRange(ctx, query, v1.Range{
				Start: now.Add(-getRandomTimeRange()),
				End:   now,
				Step:  1 * time.Minute,
			}, v1.WithTimeout(10*time.Second))
			if err != nil {
				errorCount++
				fmt.Printf("Query error in iteration %d: %v\n", i, err)
			} else {
				successCount++
			}

			// Print progress every 1000 iterations
			if (i+1)%1000 == 0 {
				fmt.Printf("Progress: %d/%d completed (Success: %d, Errors: %d, Panics: %d)\n",
					i+1, 10000, successCount, errorCount, panicCount)
			}
		}
	}()

	wg.Add(1)
	go func() {
		for i := 0; i < 10000; i++ {
			defer wg.Done()
			defer func() {
				if r := recover(); r != nil {
					panicCount++
					fmt.Printf("Panic recovered in iteration %d: %v\n", i, r)
					fmt.Printf("Stack trace:\n%s\n", debug.Stack())
				}
			}()

			expr := ps.WalkInstantQuery()
			query := expr.Pretty(0)

			_, _, err := promAPI.Query(ctx, query, now)
			if err != nil {
				errorCount++
				fmt.Printf("Query error in iteration %d: %v\n", i, err)
			} else {
				successCount++
			}

			if (i+1)%1000 == 0 {
				fmt.Printf("Progress: %d/%d completed (Success: %d, Errors: %d, Panics: %d)\n",
					i+1, 10000, successCount, errorCount, panicCount)
			}
		}
	}()

	wg.Wait()

	fmt.Printf("\nFinal results:\n")
	fmt.Printf("Total iterations: %d\n", 10000)
	fmt.Printf("Successful queries: %d\n", successCount)
	fmt.Printf("Query errors: %d\n", errorCount)
	fmt.Printf("Panics recovered: %d\n", panicCount)

	return nil
}

func modelLabelSetToLabels(labelSets []model.LabelSet) []labels.Labels {
	out := make([]labels.Labels, len(labelSets))
	bufLabels := labels.EmptyLabels()
	builder := labels.NewBuilder(bufLabels)
	for i, lbls := range labelSets {
		for k, v := range lbls {
			builder.Set(string(k), string(v))
		}
		out[i] = builder.Labels()
		builder.Reset(bufLabels)
	}
	return out
}

// Demo Prometheus is still at v2.27, some functions are not supported.
func getAvailableFunctions() []*parser.Function {
	res := make([]*parser.Function, 0)
	for _, f := range parser.Functions {
		if f.Variadic != 0 {
			continue
		}
		if slices.Contains(f.ArgTypes, parser.ValueTypeString) {
			continue
		}
		if _, ok := unsupportedFunctions[f.Name]; ok {
			continue
		}
		res = append(res, f)
	}
	return res
}

func getRandomTimeRange() time.Duration {
	timeRanges := []time.Duration{
		6 * time.Hour,
		48 * time.Hour,
		7 * 24 * time.Hour,  // 7 days
		30 * 24 * time.Hour, // 30 days
	}
	return timeRanges[rand.Intn(len(timeRanges))]
}
