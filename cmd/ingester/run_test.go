package ingester

import (
	"context"
	"database/sql"
	"flag"
	"io"
	"net"
	"net/http"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/nicolastakashi/prom-analytics-proxy/internal/config"
	"github.com/stretchr/testify/require"
	_ "modernc.org/sqlite"
)

// configure sets config.DefaultConfig from the ingester's flag defaults plus
// args, listening on ephemeral loopback ports, and restores it on cleanup.
func configure(t *testing.T, args ...string) {
	t.Helper()
	saved := *config.DefaultConfig
	t.Cleanup(func() { *config.DefaultConfig = saved })

	var configFile string
	fs := flag.NewFlagSet("ingester", flag.ContinueOnError)
	RegisterFlags(fs, &configFile)
	require.NoError(t, fs.Parse(append([]string{
		"-otlp-listen-address=127.0.0.1:0",
		"-ingester-metrics-listen-address=127.0.0.1:0",
	}, args...)))
}

// startServe runs serve until the test ends and returns the channel its
// result arrives on.
func startServe(t *testing.T) (cancel context.CancelFunc, result <-chan error) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	exited := make(chan struct{})
	go func() {
		defer close(exited)
		done <- serve(ctx)
	}()
	t.Cleanup(func() {
		cancel()
		<-exited
	})
	return cancel, done
}

func awaitResult(t *testing.T, result <-chan error, within time.Duration) error {
	t.Helper()
	select {
	case err := <-result:
		return err
	case <-time.After(within):
		require.FailNow(t, "serve did not return", "within %s", within)
		return nil
	}
}

func closedPort(t *testing.T) int {
	t.Helper()
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	port := lis.Addr().(*net.TCPAddr).Port
	require.NoError(t, lis.Close())
	return port
}

// openDBPools counts the database/sql pools open in the process: each runs
// one connectionOpener goroutine until closed.
func openDBPools() int {
	buf := make([]byte, 1<<20)
	for {
		n := runtime.Stack(buf, true)
		if n < len(buf) {
			return strings.Count(string(buf[:n]), "database/sql.(*DB).connectionOpener")
		}
		buf = make([]byte, 2*len(buf))
	}
}

// scrapedValue returns the value of series, written as in the text
// exposition format, from the /metrics body.
func scrapedValue(t *testing.T, body, series string) float64 {
	t.Helper()
	for _, line := range strings.Split(body, "\n") {
		if v, ok := strings.CutPrefix(line, series+" "); ok {
			f, err := strconv.ParseFloat(v, 64)
			require.NoError(t, err, line)
			return f
		}
	}
	require.FailNow(t, "series not exposed", series)
	return 0
}

func httpGet(t *testing.T, url string) (status int, body string) {
	t.Helper()
	client := http.Client{Timeout: 2 * time.Second}
	resp, err := client.Get(url)
	if err != nil {
		return 0, ""
	}
	defer func() { _ = resp.Body.Close() }()
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		return 0, ""
	}
	return resp.StatusCode, string(b)
}

// Not parallel: swaps config.DefaultConfig.
func TestServe_UnknownDatabaseProviderFailsStartup(t *testing.T) {
	configure(t, "-database-provider=nosuchdb")
	_, result := startServe(t)

	err := awaitResult(t, result, 10*time.Second)
	require.ErrorContains(t, err, "nosuchdb")
}

// Not parallel: swaps config.DefaultConfig.
func TestServe_UnreachablePostgreSQLServesWithoutDatabase(t *testing.T) {
	addr := "127.0.0.1:" + strconv.Itoa(closedPort(t))
	configure(t,
		"-database-provider=postgresql",
		"-postgresql-addr=127.0.0.1",
		"-postgresql-port="+strconv.Itoa(closedPort(t)),
		"-postgresql-dial-timeout=1s",
		"-ingester-metrics-listen-address="+addr,
		"-ingester-drain-delay=100ms",
	)
	cancel, result := startServe(t)
	base := "http://" + addr

	require.Eventually(t, func() bool {
		status, body := httpGet(t, base+"/readyz")
		return status == http.StatusOK && body == "ok"
	}, 10*time.Second, 20*time.Millisecond, "/readyz never answered 200 ok")
	require.Never(t, func() bool { return len(result) > 0 }, 2*time.Second, 10*time.Millisecond)
	status, body := httpGet(t, base+"/metrics")
	require.Equal(t, http.StatusOK, status)
	require.Equal(t, 0.0, scrapedValue(t, body, "ingester_database_up"))
	require.Positive(t, scrapedValue(t, body, `ingester_database_connect_attempts_total{result="failure"}`))

	cancel()
	err := awaitResult(t, result, 30*time.Second)
	require.ErrorIs(t, err, context.Canceled)
}

// Not parallel: swaps config.DefaultConfig.
func TestServe_SQLiteServesUntilCanceled(t *testing.T) {
	path := filepath.Join(t.TempDir(), "ingester.db")
	configure(t,
		"-database-provider=sqlite",
		"-sqlite-database-path="+path,
		// A drain delay longer than zero keeps the OTLP actor from returning
		// first, so the group's result is context.Canceled from an actor that
		// returns at once on cancel.
		"-ingester-drain-delay=100ms",
	)
	pools := openDBPools()
	cancel, result := startServe(t)

	require.Never(t, func() bool { return len(result) > 0 }, time.Second, 10*time.Millisecond)
	cancel()
	err := awaitResult(t, result, 30*time.Second)
	require.ErrorIs(t, err, context.Canceled)
	require.Eventually(t, func() bool { return openDBPools() == pools }, 5*time.Second, 10*time.Millisecond,
		"database pool left open")

	db, err := sql.Open("sqlite", path)
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	var name string
	require.NoError(t, db.QueryRow(`SELECT name FROM sqlite_master WHERE type = 'table' AND name = 'queries'`).Scan(&name))
}

// Not parallel: swaps config.DefaultConfig.
func TestServe_ListenAddressInUseFailsStartup(t *testing.T) {
	for _, flag := range []string{"-ingester-metrics-listen-address", "-otlp-listen-address"} {
		t.Run(flag, func(t *testing.T) {
			lis, err := net.Listen("tcp", "127.0.0.1:0")
			require.NoError(t, err)
			t.Cleanup(func() { _ = lis.Close() })
			configure(t,
				"-database-provider=sqlite",
				"-sqlite-database-path="+filepath.Join(t.TempDir(), "ingester.db"),
				flag+"="+lis.Addr().String(),
				"-ingester-drain-delay=0s",
			)
			_, result := startServe(t)

			err = awaitResult(t, result, 30*time.Second)
			require.ErrorContains(t, err, lis.Addr().String())
		})
	}
}

// Not parallel: swaps config.DefaultConfig.
func TestServe_UnknownProtocolFailsStartup(t *testing.T) {
	configure(t,
		"-database-provider=sqlite",
		"-sqlite-database-path="+filepath.Join(t.TempDir(), "ingester.db"),
		"-ingester-protocol=nosuchprotocol",
	)
	_, result := startServe(t)

	err := awaitResult(t, result, 10*time.Second)
	require.ErrorContains(t, err, "unknown protocol: nosuchprotocol")
}

// Not parallel: swaps config.DefaultConfig.
func TestServe_ExposesHealthAndMetrics(t *testing.T) {
	addr := "127.0.0.1:" + strconv.Itoa(closedPort(t))
	configure(t,
		"-database-provider=sqlite",
		"-sqlite-database-path="+filepath.Join(t.TempDir(), "ingester.db"),
		"-ingester-metrics-listen-address="+addr,
		"-ingester-drain-delay=0s",
	)
	startServe(t)
	base := "http://" + addr

	require.Eventually(t, func() bool {
		status, body := httpGet(t, base+"/livez")
		return status == http.StatusOK && body == "ok"
	}, 10*time.Second, 20*time.Millisecond, "/livez never answered 200 ok")
	require.Eventually(t, func() bool {
		status, body := httpGet(t, base+"/readyz")
		return status == http.StatusOK && body == "ok"
	}, 10*time.Second, 20*time.Millisecond, "/readyz never answered 200 ok")

	status, body := httpGet(t, base+"/metrics")
	require.Equal(t, http.StatusOK, status)
	require.Contains(t, body, "go_goroutines")

	status, _ = httpGet(t, base+"/debug/pprof/")
	require.Equal(t, http.StatusOK, status)

	status, _ = httpGet(t, base+"/nosuchpath")
	require.Equal(t, http.StatusNotFound, status)
}
