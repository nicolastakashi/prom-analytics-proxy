package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"math/rand"
	"os"
	"runtime/debug"
	"strings"
	"sync"
	"sync/atomic"
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

type config struct {
	prometheusURL    string
	iterations       int
	duration         time.Duration
	progressInterval time.Duration
	logErrors        bool
	workers          int
}

func main() {
	cfg := &config{}
	var durationStr string
	var progressIntervalStr string

	flag.StringVar(&cfg.prometheusURL, "url", "http://localhost:9091", "Prometheus URL")
	flag.IntVar(&cfg.iterations, "iterations", 0, "Number of iterations per query type (0 = unlimited with duration)")
	flag.StringVar(&durationStr, "duration", "5m", "Run duration (e.g., 30s, 5m, 1h). Takes precedence over iterations.")
	flag.StringVar(&progressIntervalStr, "progress-interval", "10s", "Progress reporting interval (e.g., 5s, 30s, 1m)")
	flag.BoolVar(&cfg.logErrors, "log-errors", false, "Log error details to stderr")
	flag.IntVar(&cfg.workers, "workers", 2, "Number of concurrent workers (1=range only, 2=range+instant)")
	flag.Parse()

	var err error
	cfg.duration, err = time.ParseDuration(durationStr)
	if err != nil {
		slog.Error("invalid duration", "error", err)
		os.Exit(1)
	}

	cfg.progressInterval, err = time.ParseDuration(progressIntervalStr)
	if err != nil {
		slog.Error("invalid progress-interval", "error", err)
		os.Exit(1)
	}

	if cfg.duration <= 0 && cfg.iterations <= 0 {
		slog.Error("either duration or iterations must be set")
		os.Exit(1)
	}

	if err := run(cfg); err != nil {
		slog.Error("failed to run", "error", err)
		os.Exit(1)
	}
}

type errorCategory struct {
	badData    atomic.Int64
	manyToMany atomic.Int64
	regexError atomic.Int64
	timeout    atomic.Int64
	other      atomic.Int64
}

type stats struct {
	successCount atomic.Int64
	errorCount   atomic.Int64
	panicCount   atomic.Int64
	totalCount   atomic.Int64
	errorCat     errorCategory
}

func (s *stats) recordSuccess() {
	s.successCount.Add(1)
	s.totalCount.Add(1)
}

func (s *stats) recordError(err error) {
	s.errorCount.Add(1)
	s.totalCount.Add(1)

	errStr := err.Error()
	switch {
	case strings.Contains(errStr, "bad_data"):
		s.errorCat.badData.Add(1)
	case strings.Contains(errStr, "many-to-many matching"):
		s.errorCat.manyToMany.Add(1)
	case strings.Contains(errStr, "error parsing regexp"):
		s.errorCat.regexError.Add(1)
	case strings.Contains(errStr, "timeout") || strings.Contains(errStr, "context deadline exceeded"):
		s.errorCat.timeout.Add(1)
	default:
		s.errorCat.other.Add(1)
	}
}

func (s *stats) recordPanic() {
	s.panicCount.Add(1)
	s.totalCount.Add(1)
}

func (s *stats) print(label string) {
	total := s.totalCount.Load()
	success := s.successCount.Load()
	errors := s.errorCount.Load()
	panics := s.panicCount.Load()

	if total == 0 {
		fmt.Printf("%s - No data yet\n", label)
		return
	}

	fmt.Printf("%s - Total: %d, Success: %d (%.1f%%), Errors: %d (%.1f%%), Panics: %d (%.1f%%)\n",
		label,
		total,
		success, float64(success)/float64(total)*100,
		errors, float64(errors)/float64(total)*100,
		panics, float64(panics)/float64(total)*100,
	)
}

func (s *stats) printDetailed() {
	fmt.Printf("\n=== Detailed Error Breakdown ===\n")
	fmt.Printf("Bad Data (resolution/invalid): %d\n", s.errorCat.badData.Load())
	fmt.Printf("Many-to-Many Matching: %d\n", s.errorCat.manyToMany.Load())
	fmt.Printf("Regex Errors: %d\n", s.errorCat.regexError.Load())
	fmt.Printf("Timeouts: %d\n", s.errorCat.timeout.Load())
	fmt.Printf("Other Errors: %d\n", s.errorCat.other.Load())
}

func run(cfg *config) error {
	client, err := api.NewClient(api.Config{
		Address: cfg.prometheusURL,
	})

	if err != nil {
		return errors.Wrapf(err, "create Prometheus client")
	}

	promAPI := v1.NewAPI(client)
	ctx := context.Background()
	now := time.Now()

	fmt.Printf("Fetching series from %s...\n", cfg.prometheusURL)
	series, _, err := promAPI.Series(
		ctx,
		[]string{"{job!=\"\"}"},
		now.Add(-2*time.Hour), now,
	)
	if err != nil {
		return errors.Wrapf(err, "get series")
	}
	fmt.Printf("Found %d series\n", len(series))

	rnd := rand.New(rand.NewSource(now.Unix()))
	opts := []promqlsmith.Option{
		promqlsmith.WithEnableOffset(true),
		promqlsmith.WithEnableAtModifier(true),
		promqlsmith.WithEnabledFunctions(getAvailableFunctions()),
		promqlsmith.WithEnabledBinOps(enabledBinops),
		promqlsmith.WithEnableVectorMatching(true),
	}

	ps := promqlsmith.New(rnd, modelLabelSetToLabels(series), opts...)

	st := &stats{}
	startTime := time.Now()

	var endTime time.Time
	if cfg.duration > 0 {
		endTime = startTime.Add(cfg.duration)
		fmt.Printf("\nRunning for %s (until %s)\n", cfg.duration, endTime.Format("15:04:05"))
	} else {
		fmt.Printf("\nRunning %d iterations per worker\n", cfg.iterations)
	}
	fmt.Printf("Workers: %d\n", cfg.workers)
	fmt.Printf("Progress interval: %s\n\n", cfg.progressInterval)

	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	wg := sync.WaitGroup{}

	wg.Add(1)
	go func() {
		defer wg.Done()
		runRangeQueries(runCtx, promAPI, ps, now, cfg.iterations, cfg.duration, endTime, cfg.progressInterval, cfg.logErrors, st)
	}()

	if cfg.workers > 1 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			runInstantQueries(runCtx, promAPI, ps, now, cfg.iterations, cfg.duration, endTime, cfg.progressInterval, cfg.logErrors, st)
		}()
	}

	wg.Wait()

	elapsed := time.Since(startTime)

	fmt.Printf("\n=== Final Results ===\n")
	st.print("Combined")
	st.printDetailed()
	fmt.Printf("\nTotal queries executed: %d\n", st.totalCount.Load())
	fmt.Printf("Elapsed time: %s\n", elapsed.Round(time.Second))
	fmt.Printf("Queries per second: %.2f\n", float64(st.totalCount.Load())/elapsed.Seconds())

	return nil
}

func runRangeQueries(ctx context.Context, promAPI v1.API, ps *promqlsmith.PromQLSmith, now time.Time, iterations int, duration time.Duration, endTime time.Time, progressInterval time.Duration, logErrors bool, st *stats) {
	useDuration := duration > 0
	lastProgress := time.Now()
	i := 0

	for {
		if useDuration {
			if time.Now().After(endTime) {
				break
			}
		} else {
			if i >= iterations {
				break
			}
		}

		select {
		case <-ctx.Done():
			return
		default:
		}

		func() {
			defer func() {
				if r := recover(); r != nil {
					st.recordPanic()
					if logErrors {
						fmt.Fprintf(os.Stderr, "[Range] Panic in iteration %d: %v\n", i, r)
						fmt.Fprintf(os.Stderr, "Stack trace:\n%s\n", debug.Stack())
					}
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
				st.recordError(err)
				if logErrors {
					fmt.Fprintf(os.Stderr, "[Range] Error in iteration %d: %v\nQuery: %s\n", i, err, query)
				}
			} else {
				st.recordSuccess()
			}
		}()

		i++

		if time.Since(lastProgress) >= progressInterval {
			if useDuration {
				remaining := time.Until(endTime)
				st.print(fmt.Sprintf("[Range] %d queries, %s remaining", i, remaining.Round(time.Second)))
			} else {
				st.print(fmt.Sprintf("[Range] Progress %d/%d", i, iterations))
			}
			lastProgress = time.Now()
		}
	}
}

func runInstantQueries(ctx context.Context, promAPI v1.API, ps *promqlsmith.PromQLSmith, now time.Time, iterations int, duration time.Duration, endTime time.Time, progressInterval time.Duration, logErrors bool, st *stats) {
	useDuration := duration > 0
	lastProgress := time.Now()
	i := 0

	for {
		if useDuration {
			if time.Now().After(endTime) {
				break
			}
		} else {
			if i >= iterations {
				break
			}
		}

		select {
		case <-ctx.Done():
			return
		default:
		}

		func() {
			defer func() {
				if r := recover(); r != nil {
					st.recordPanic()
					if logErrors {
						fmt.Fprintf(os.Stderr, "[Instant] Panic in iteration %d: %v\n", i, r)
						fmt.Fprintf(os.Stderr, "Stack trace:\n%s\n", debug.Stack())
					}
				}
			}()

			expr := ps.WalkInstantQuery()
			query := expr.Pretty(0)

			_, _, err := promAPI.Query(ctx, query, now)
			if err != nil {
				st.recordError(err)
				if logErrors {
					fmt.Fprintf(os.Stderr, "[Instant] Error in iteration %d: %v\nQuery: %s\n", i, err, query)
				}
			} else {
				st.recordSuccess()
			}
		}()

		i++

		if time.Since(lastProgress) >= progressInterval {
			if useDuration {
				remaining := time.Until(endTime)
				st.print(fmt.Sprintf("[Instant] %d queries, %s remaining", i, remaining.Round(time.Second)))
			} else {
				st.print(fmt.Sprintf("[Instant] Progress %d/%d", i, iterations))
			}
			lastProgress = time.Now()
		}
	}
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
