# Leader election

`internal/leaderelection` coordinates single-leader execution of the inventory syncer and the retention worker across multiple replicas of `prom-analytics-proxy` sharing one PostgreSQL database. Only one replica runs each of these jobs at a time; the rest wait, and one of them takes over automatically if the current leader disappears. Two strategies are available, selected by `-leader-election-strategy`: `advisory-lock` (the default) and `lease`.

What those two jobs actually do, how they're scheduled, and the time budget each cycle runs under is [docs/jobs.md](jobs.md)'s concern — this doc covers only who gets to run them.

## Why this exists

Both the inventory syncer (`internal/inventory`) and the retention worker (`internal/retention`) periodically read and write shared state in PostgreSQL — the metrics catalog, usage summaries, and old-data cleanup. Running them from every replica simultaneously would mean duplicate work at best and races against shared rows at worst. Leader election picks exactly one replica to run each job; every other replica polls in the background, ready to take over.

## Why coordination is PostgreSQL only

Both coordinating strategies below need shared state, and they only exist for `-database-provider=postgresql`: advisory locks are a PostgreSQL feature, and the `leader_leases` table is created only by the PostgreSQL migrations. That isn't a gap for SQLite — SQLite mode in this project is single-instance-per-file by deployment convention (each replica has its own local database file; there is no shared file for multiple processes to coordinate access to), so there is nothing for a leader-election mechanism to arbitrate.

What SQLite gets instead is `NewSoleInstance`: an `Elector` that holds leadership unconditionally, touching no lock, no lease row and no table — which is why it needs no `*sql.DB` and works on a database whose schema has neither. Every provider therefore has an elector, so `cmd/api` wires a job exactly one way rather than scheduling it itself wherever nothing coordinates it, and the leadership metrics below exist in every deployment: a fleet-wide "no leader for this lease" panel would otherwise silently exclude every replica not using PostgreSQL.

## Strategy: advisory-lock (default)

`NewAdvisoryElector` implements `Elector` using PostgreSQL's session-level advisory locks (`pg_try_advisory_lock`). Each replica repeatedly attempts to acquire a lock keyed by the lease name (`"metric-analytics-inventory"`, `"metric-analytics-retention"`); the one that succeeds runs the job for as long as it holds the lock, and every other replica backs off and retries.

A session-level advisory lock is tied to the *physical Postgres backend session* behind a specific connection, not to the Go-level `*sql.Conn` handle. Releasing it correctly means issuing `pg_advisory_unlock` on that same connection before closing it — relying on `conn.Close()` alone is not sufficient, because `database/sql`'s `Close()` returns a connection to the pool for potential reuse rather than guaranteeing the physical session is torn down. If the connection that held the lock were ever recycled, or its owning process disappeared without the socket being observably closed, the lock would stay held by nobody, forever, permanently blocking every future leader election for that lease name. `internal/leaderelection`'s advisory-lock strategy always calls `pg_advisory_unlock` explicitly as part of releasing leadership, so release never depends on connection-pool behavior — and if that unlock call itself fails, or reports the lock wasn't actually released, the connection is force-discarded (never returned to the pool) rather than trusted: Postgres tearing down the discarded session releases the lock as a side effect, closing the same leak from the other direction.

While leadership is held, a background check periodically pings the same connection and steps down (cancels the caller's leadership context) the moment that ping fails. Without it, a backend session that dies without the client noticing — a Postgres restart, `pg_terminate_backend`, a half-open network partition — would free the advisory lock immediately on the server side while this replica kept believing it was still leader, opening a window for two replicas to run the job concurrently.

A panic in the elected job itself is also caught and re-raised after release runs, so the lock unlocks over the still-live connection before the panic propagates and (with no recovery elsewhere in this process) crashes it — release here doesn't depend on Postgres ever noticing a dead session. That guarantee doesn't extend to the job's *process* disappearing outright (a hard kill, an OOM-kill, the host itself dying): nothing in application code runs in that case, and how long the lock then stays held from Postgres's perspective is entirely a function of TCP keepalive settings on that connection, not of anything this package controls.

The retry loop treats "someone else currently holds it" (`ok=false, err=nil`) and a genuine error (`err!=nil`, e.g. a dropped connection or a query failure) differently: contention polls at the fixed initial interval for as long as it persists — since that's the steady state for every follower, growing it would regress failover latency — while errors back off with growing (jittered) delays up to a configured cap, and are logged. Either way, the loop only ever stops, returning `nil`, when the caller's own `ctx` is canceled. A leader-election loop that returns early on an ordinary, retryable error is a correctness bug on its own (every other replica is left with no leader at all until the process restarts), so this package treats "stop retrying" and "ctx canceled" as the same event by construction, rather than as two cases that happen to behave the same way today.

Requires no schema beyond what `internal/db`'s migrations already provide, and needs no configuration — this is why it's the default.

## Strategy: lease

`newLeaseStrategy` implements `strategy` using a row in a `leader_leases` table instead of a session-level lock. A lease's validity is a plain timestamp (`expires_at`) compared server-side against `now()` — its correctness has nothing to do with any connection's physical lifecycle, which is the property the advisory-lock strategy has to work hard (explicit `pg_advisory_unlock`) to get right. If a holder disappears without ever releasing — crash, panic, a killed pod — it simply stops renewing, and any other replica can take over automatically as soon as `expires_at` passes. Nothing needs to notice the holder is gone; there's no "held by nobody, forever" state to reach in the first place.

Acquiring and renewing are the same one-round-trip SQL statement (`INSERT ... ON CONFLICT (lease_name) DO UPDATE ... WHERE ...`): it succeeds if the lease is free, expired, or already held by the caller, and does nothing (so `RETURNING` yields no row) if it's live and held by someone else. Every comparison runs server-side (`now()`, `make_interval()`), so acquisition can't be affected by clock skew between replicas.

Each successful acquire starts a background renewal goroutine that re-runs the same statement every `-leader-election-renew-interval`, for as long as the caller holds leadership. It cancels the context passed into the running job the instant leadership is actually lost — the running job needs to stop mid-run, not just be noticed at its next natural checkpoint — but "actually lost" is deliberately narrower than "a renewal attempt failed": losing the lease to a different holder is confirmed by the database itself (the renewal statement's `WHERE` clause matched no row) and cancels immediately, while a database error (a dropped connection, a query timeout) says nothing about who holds the lease and is instead retried on the next tick. Only once the time since the last successful renewal reaches the TTL — meaning the lease may genuinely have expired server-side by then — does the watchdog give up. Canceling on every transient error would throw away the TTL margin the renew interval exists to provide, aborting in-flight work over a blip the lease was sized to absorb.

A graceful step-down (releasing while still the holder — a normal rolling deploy, not a crash) proactively expires the lease instead of leaving the next holder to wait out the full TTL: release sets `expires_at` into the past, conditioned on still owning the row, so a race with a holder that's already taken over is a harmless no-op.

Each acquired lease also carries a `fence_token`, drawn from a dedicated sequence (`leader_lease_fence_token_seq`) every time the lease changes hands (not on a same-holder renewal). A downstream consumer that wants to reject writes from a holder that has since lost the lease can compare the token it acquired with against the current one — this package doesn't use the token for anything itself today, but the column exists so that guarantee is available without a schema change later. The token is sourced from a sequence rather than incrementing the row's own previous value specifically so that guarantee survives the row itself being deleted and recreated — an operator forcing a failover, a disaster-recovery cleanup — which a per-row counter cannot: it would restart at its initial value on the next fresh insert, letting a reissued token collide with or fall below one a consumer already saw.

`leader_leases` is created by `internal/db`'s own migrations (`internal/db/migrations/postgresql/0015_leader_leases.sql`), alongside every other table this project uses — the same schema-versioning mechanism, not a separate one. It's created unconditionally on every PostgreSQL deployment regardless of which strategy is selected: one small, empty, unused table on advisory-lock deployments costs essentially nothing, and it keeps schema state independent of which feature flag happens to be set, rather than depending on it.

### Choosing between them

| | `advisory-lock` (default) | `lease` |
|---|---|---|
| Schema footprint | none | one table, created on first use |
| Configuration | none | `-leader-election-lease-ttl`, `-leader-election-renew-interval` |
| Recovery from an ungraceful holder death | depends on the OS/driver eventually closing the dead connection | bounded by the TTL, always |
| Recovery from a live process whose job is stuck | no — only a dead process/connection recovers | yes, for a job that reports its cycles — see [Reporting job cycles](#reporting-job-cycles) |

`-leader-election-lease-ttl` (default `15s`) and `-leader-election-renew-interval` (default `5s`, must be strictly less than the TTL — `New` rejects a config where it isn't) control how quickly a lease strategy notices and recovers from a dead holder versus how much renewal traffic it generates. A renew interval close to the TTL risks a healthy holder losing its own lease to a slow renewal round-trip; the default 3:1 ratio leaves comfortable margin.

## Reporting job cycles

Renewal answers one question: is this replica's process still alive. It says nothing about whether the elected job running under that leadership is actually making progress, since renewal and the job are separate goroutines with no connection between them.

`CycleReporter` is the seam for closing that gap: `Elector.Run`'s `fn` receives one alongside its context, and can call `CycleStarted(cancel, budget)` to hand over one unit of work's own cancel func and declared maximum duration. `PeriodicJob(interval, budget, cycle)` builds an `fn` of this shape from a plain `func(context.Context)` - a job's per-cycle logic - so a job never needs to know `CycleReporter` exists at all; it just exposes one function and its own budget, the same as it would for any other periodic caller.

A reporter is only useful to a strategy that acts on it. Advisory-lock hands it straight through unused - it has no equivalent renewal-vs-job gap to close in the first place. The lease strategy's watchdog checks it on every tick, before ever touching the renewal round trip: once a cycle that is *still running* has been going longer than its declared budget, it cancels that cycle's own context and steps down exactly as if the lease had been lost to another holder - the same graceful-release path an ordinary lost lease already takes, not a new one. A cycle that already returned is never a violation, however long ago it started: a job whose interval is longer than its budget is idle past that budget on every cycle, and the reported cycle's own context - canceled by `PeriodicJob` as soon as the cycle returns - is what tells the two apart. A job that never reports a cycle at all - a raw `fn` bypassing `PeriodicJob` - is simply unaffected, the same as under advisory-lock: the watchdog has nothing to compare against.

Stepping down this way means the *same* replica usually wins the next acquisition immediately - it isn't backed off the way every other follower is, so in practice this recovers a stuck cycle on its own process. If the same replica is itself the actual problem, it will keep hitting the same budget and keep stepping down and retrying rather than staying stuck forever.

**What this doesn't cover:** a cycle that hangs on something that never touches its context at all - a genuine Go-level deadlock, not a slow or cancelable operation - can't be reached by any context-cancellation-based mechanism, this one included; canceling a context only affects code that's actually watching for it.

Both of `cmd/api`'s elected jobs are wired through `PeriodicJob` on the PostgreSQL path: `internal/inventory.Syncer.RunOnce` and `internal/retention.Worker.RunOnce` are each a plain `func(context.Context)` - one sync/cleanup cycle, no looping, no knowledge of leader election. The budget `PeriodicJob` reports is each job's own `run_timeout`, read straight from the config: startup settles that value first (see docs/jobs.md), so it already is the bound the job's cycles run under, and this package derives nothing of its own. Were it read before settling, the watchdog could enforce a bound tighter than the cycle it is watching. Neither `internal/inventory` nor `internal/retention` imports this one; `cmd/api` is the only place that connects a job's plain per-cycle function to `PeriodicJob`.

## Metrics

- `leaderelection_is_leader` (gauge, label `lease_name`) — `1` while this process holds leadership for that lease, `0` otherwise.
- `leaderelection_transitions_total` (counter, labels `lease_name`, `to` ∈ `{leader, follower}`) — incremented on every leadership transition.

Both are emitted generically by the `Elector` wrapper — the lease strategy gets this instrumentation for free, identically to the advisory-lock strategy, and so will any strategy added after it.

A cycle canceled for exceeding its declared budget logs `"job cycle exceeded its declared budget; canceling and stepping down"` (`slog.Warn`, with `lease_name`) and counts as an ordinary leader→follower transition in `leaderelection_transitions_total`.

Example: find any lease with no current leader (a healthy fleet should always show exactly one leader per lease name). Every replica exports `0` for a lease it isn't leading, not just `1` for the one that is, so a leaderless lease still has series to query against — `max` across them is `0`:
```
max by (lease_name) (leaderelection_is_leader) == 0
```
