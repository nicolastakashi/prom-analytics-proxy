# Leader election

`internal/leaderelection` coordinates single-leader execution of the inventory syncer and the retention worker across multiple replicas of `prom-analytics-proxy` sharing one PostgreSQL database. Only one replica runs each of these jobs at a time; the rest wait, and one of them takes over automatically if the current leader disappears.

## Why this exists

Both the inventory syncer (`internal/inventory`) and the retention worker (`internal/retention`) periodically read and write shared state in PostgreSQL — the metrics catalog, usage summaries, and old-data cleanup. Running them from every replica simultaneously would mean duplicate work at best and races against shared rows at worst. Leader election picks exactly one replica to run each job; every other replica polls in the background, ready to take over.

## Why PostgreSQL only

This package is only used when `-database-provider=postgresql`. When running with SQLite, `cmd/api`'s wiring skips leader election entirely and runs the inventory syncer and retention worker directly on every replica. This isn't a gap — SQLite mode in this project is single-instance-per-file by deployment convention (each replica has its own local database file; there is no shared file for multiple processes to coordinate access to), so there's nothing for a leader-election mechanism to arbitrate.

## How it works today: PostgreSQL advisory locks

`NewAdvisoryElector` implements `Elector` using PostgreSQL's session-level advisory locks (`pg_try_advisory_lock`). Each replica repeatedly attempts to acquire a lock keyed by the lease name (`"metric-analytics-inventory"`, `"metric-analytics-retention"`); the one that succeeds runs the job for as long as it holds the lock, and every other replica backs off and retries.

A session-level advisory lock is tied to the *physical Postgres backend session* behind a specific connection, not to the Go-level `*sql.Conn` handle. Releasing it correctly means issuing `pg_advisory_unlock` on that same connection before closing it — relying on `conn.Close()` alone is not sufficient, because `database/sql`'s `Close()` returns a connection to the pool for potential reuse rather than guaranteeing the physical session is torn down. If the connection that held the lock were ever recycled, or its owning process disappeared without the socket being observably closed, the lock would stay held by nobody, forever, permanently blocking every future leader election for that lease name. `internal/leaderelection`'s advisory-lock strategy always calls `pg_advisory_unlock` explicitly as part of releasing leadership, so release never depends on connection-pool behavior — and if that unlock call itself fails, or reports the lock wasn't actually released, the connection is force-discarded (never returned to the pool) rather than trusted: Postgres tearing down the discarded session releases the lock as a side effect, closing the same leak from the other direction.

While leadership is held, a background check periodically pings the same connection and steps down (cancels the caller's leadership context) the moment that ping fails. Without it, a backend session that dies without the client noticing — a Postgres restart, `pg_terminate_backend`, a half-open network partition — would free the advisory lock immediately on the server side while this replica kept believing it was still leader, opening a window for two replicas to run the job concurrently.

A panic in the elected job itself is also caught and re-raised after release runs, so the lock unlocks over the still-live connection before the panic propagates and (with no recovery elsewhere in this process) crashes it — release here doesn't depend on Postgres ever noticing a dead session. That guarantee doesn't extend to the job's *process* disappearing outright (a hard kill, an OOM-kill, the host itself dying): nothing in application code runs in that case, and how long the lock then stays held from Postgres's perspective is entirely a function of TCP keepalive settings on that connection, not of anything this package controls.

The retry loop treats "someone else currently holds it" (`ok=false, err=nil`) and a genuine error (`err!=nil`, e.g. a dropped connection or a query failure) differently: contention polls at the fixed initial interval for as long as it persists — since that's the steady state for every follower, growing it would regress failover latency — while errors back off with growing (jittered) delays up to a configured cap, and are logged. Either way, the loop only ever stops, returning `nil`, when the caller's own `ctx` is canceled. A leader-election loop that returns early on an ordinary, retryable error is a correctness bug on its own (every other replica is left with no leader at all until the process restarts), so this package treats "stop retrying" and "ctx canceled" as the same event by construction, rather than as two cases that happen to behave the same way today.

## Metrics

- `leaderelection_is_leader` (gauge, label `lease_name`) — `1` while this process holds leadership for that lease, `0` otherwise.
- `leaderelection_transitions_total` (counter, labels `lease_name`, `to` ∈ `{leader, follower}`) — incremented on every leadership transition.

Both are emitted generically by the `Elector` wrapper, so any future leader-election strategy gets this instrumentation for free.

Example: find any lease with no current leader (a healthy fleet should always show exactly one leader per lease name). Every replica exports `0` for a lease it isn't leading, not just `1` for the one that is, so a leaderless lease still has series to query against — `max` across them is `0`:
```
max by (lease_name) (leaderelection_is_leader) == 0
```
