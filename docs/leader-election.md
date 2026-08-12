# Leader election

`internal/leaderelection` coordinates single-leader execution of the inventory syncer and the retention worker across multiple replicas of `prom-analytics-proxy` sharing one PostgreSQL database. Only one replica runs each of these jobs at a time; the rest wait, and one of them takes over automatically if the current leader disappears.

## Why this exists

Both the inventory syncer (`internal/inventory`) and the retention worker (`internal/retention`) periodically read and write shared state in PostgreSQL — the metrics catalog, usage summaries, and old-data cleanup. Running them from every replica simultaneously would mean duplicate work at best and races against shared rows at worst. Leader election picks exactly one replica to run each job; every other replica polls in the background, ready to take over.

## Why PostgreSQL only

This package is only used when `-database-provider=postgresql`. When running with SQLite, `cmd/api`'s wiring skips leader election entirely and runs the inventory syncer and retention worker directly on every replica. This isn't a gap — SQLite mode in this project is single-instance-per-file by deployment convention (each replica has its own local database file; there is no shared file for multiple processes to coordinate access to), so there's nothing for a leader-election mechanism to arbitrate.

## How it works today: PostgreSQL advisory locks

`NewAdvisoryElector` implements `Elector` using PostgreSQL's session-level advisory locks (`pg_try_advisory_lock`). Each replica repeatedly attempts to acquire a lock keyed by the lease name (`"metric-analytics-inventory"`, `"metric-analytics-retention"`); the one that succeeds runs the job for as long as it holds the lock, and every other replica backs off and retries.

A session-level advisory lock is tied to the *physical Postgres backend session* behind a specific connection, not to the Go-level `*sql.Conn` handle. Releasing it correctly means issuing `pg_advisory_unlock` on that same connection before closing it — relying on `conn.Close()` alone is not sufficient, because `database/sql`'s `Close()` returns a connection to the pool for potential reuse rather than guaranteeing the physical session is torn down. If the connection that held the lock were ever recycled, or its owning process disappeared without the socket being observably closed, the lock would stay held by nobody, forever, permanently blocking every future leader election for that lease name. `internal/leaderelection`'s advisory-lock strategy always calls `pg_advisory_unlock` explicitly as part of releasing leadership, so release never depends on connection-pool behavior.

The retry loop applies the same treatment to every failure to acquire or hold leadership — whether that's "someone else currently holds it" or a genuine error (a dropped connection, a query failure). Both back off and retry identically; the loop only ever stops, returning `nil`, when the caller's own `ctx` is canceled. A leader-election loop that returns early on an ordinary, retryable error is a correctness bug on its own (every other replica is left with no leader at all until the process restarts), so this package treats "stop retrying" and "ctx canceled" as the same event by construction, rather than as two cases that happen to behave the same way today.

## Metrics

- `leaderelection_is_leader` (gauge, label `lease_name`) — `1` while this process holds leadership for that lease, `0` otherwise.
- `leaderelection_transitions_total` (counter, labels `lease_name`, `to` ∈ `{leader, follower}`) — incremented on every leadership transition.

Both are emitted generically by the `Elector` wrapper, so any future leader-election strategy gets this instrumentation for free.

Example: find any lease with no current leader (a healthy fleet should always show exactly one leader per lease name):
```
count by (lease_name) (leaderelection_is_leader == 1)
```
