# Troubleshooting Guide

This guide covers common issues and their solutions when running `prom-analytics-proxy`.

## Table of Contents

- [No Data Showing in the UI](#no-data-showing-in-the-ui)
- [Context Deadline Exceeded Errors](#context-deadline-exceeded-errors)
- [Proxy Performance Issues](#proxy-performance-issues)
- [Missing Metrics in Inventory](#missing-metrics-in-inventory)
- [Database Connection Issues](#database-connection-issues)
- [Memory/Resource Issues](#memoryresource-issues)

## No Data Showing in the UI

**Symptom:** The web UI shows no queries, or the query count remains at zero even though you're actively using Grafana/Perses.

**Cause:** Your query clients are not sending traffic through the proxy. They're still querying your metrics backend directly.

**Solution:**

1. Verify your query clients (Grafana, Perses, etc.) are configured to use the proxy URL (`:9091` by default)
2. Check the proxy logs - you should see query traffic being logged when queries are executed
3. Test the proxy directly:

   ```bash
   curl "http://localhost:9091/api/v1/query?query=up"
   ```

   If this works but you still see no data in the UI, check your database connection

4. Verify the proxy is actually receiving requests by checking the logs:

   ```bash
   # You should see log entries like this when queries are proxied:
   level=INFO msg="proxying query" query="up" ...
   ```

### Example from Issue [#386](https://github.com/nicolastakashi/prom-analytics-proxy/issues/386)

A common scenario that causes this issue:

- User was port-forwarding to Thanos Query on `localhost:9090`
- Started the proxy pointing to `http://localhost:9090`
- **But never reconfigured Grafana to use the proxy** - Grafana was still querying Thanos directly
- Result: No query traffic through proxy = no analytics data

### Additional Checks

- Ensure there are no firewall rules blocking connections to port 9091
- Verify the proxy is binding to the correct network interface (use `-insecure-listen-address "0.0.0.0:9091"` to listen on all interfaces)
- Check if your query client has the old Prometheus URL cached

## Context Deadline Exceeded Errors

**Symptom:** Logs show many `context deadline exceeded` errors, especially during inventory sync:

```text
level=WARN msg="failed to query metrics for job" job=apiserver query="..." err="context deadline exceeded"
```

**Cause:** Your metrics backend (especially Thanos with large datasets) may be slow to respond, and the default timeouts are too aggressive.

**Solution:**

### Option 1: Increase Inventory Timeouts

For moderate-sized deployments:

```yaml
inventory-job-index-per-job-timeout: 60s  # Default: 30s
inventory-job-index-label-timeout: 60s     # Default: 30s
inventory-run-timeout: 10m                 # Default: 5m
```

Or via command line:

```bash
./prom-analytics-proxy \
  -upstream http://prometheus:9090 \
  -inventory-job-index-per-job-timeout 60s \
  -inventory-job-index-label-timeout 60s \
  -inventory-run-timeout 10m
```

### Option 2: Scale for Large Deployments

For very large deployments with hundreds of jobs:

```yaml
inventory-job-index-workers: 20            # Default: 10 (more parallel processing)
inventory-job-index-per-job-timeout: 120s  # Longer timeout per job
inventory-run-timeout: 30m                 # Much longer overall timeout
```

### Option 3: Reduce Time Window

Consider reducing the time window for initial sync:

```yaml
inventory-time-window: 168h  # 7 days instead of default 30 days
```

This makes the initial sync faster by analyzing a shorter time period.

### Option 4: Disable Inventory Temporarily

If inventory sync is consistently failing and you only need query analytics:

```bash
./prom-analytics-proxy \
  -upstream http://prometheus:9090 \
  -inventory-enabled=false
```

### Understanding the Error

The proxy makes several types of queries during inventory sync:

1. **Label queries** - to discover all job labels
2. **Per-job queries** - to find all metrics for each job
3. **Metadata queries** - to get metric types and descriptions

Each of these has its own timeout. If your Prometheus/Thanos instance is slow or has many metrics, these queries may timeout.

## Proxy Performance Issues

**Symptom:** Queries through the proxy are noticeably slower than direct queries.

**Cause:** Database insert operations may be blocking query responses.

**Solution:**

### Option 1: Tune Insert Buffer Settings

Increase buffering to reduce database write pressure:

```yaml
insert-buffer-size: 1000      # Default: 100
insert-batch-size: 50         # Default: 10
insert-flush-interval: 10s    # Default: 5s
```

This allows more queries to be buffered in memory before writing to the database.

### Option 2: Switch to PostgreSQL

If using SQLite, consider switching to PostgreSQL for better concurrent write performance:

```yaml
database-provider: "postgresql"
postgresql-addr: "postgres.example.com"
postgresql-port: 5432
postgresql-database: "prom_analytics"
```

PostgreSQL handles concurrent writes much better than SQLite.

### Option 3: Check Database Performance

1. Monitor database disk I/O - if saturated, consider faster storage
2. Ensure the database has adequate resources (CPU, memory)
3. Check for missing indexes (migrations should create these automatically)

### Option 4: Disable Query Stats

If you don't need detailed query statistics:

```bash
./prom-analytics-proxy \
  -upstream http://prometheus:9090 \
  -include-query-stats=false
```

This reduces the overhead of capturing query statistics.

### Measuring Impact

To measure the proxy's overhead:

1. Time a query directly to Prometheus: `time curl "http://prometheus:9090/api/v1/query?query=up"`
2. Time the same query through the proxy: `time curl "http://localhost:9091/api/v1/query?query=up"`
3. The difference should typically be <50ms

## Missing Metrics in Inventory

**Symptom:** The metrics inventory is incomplete or missing expected metrics.

**Cause:** The metadata limit may be too low, or inventory sync may be failing.

**Solution:**

### Option 1: Increase Metadata Limit

```bash
./prom-analytics-proxy \
  -metadata-limit 100000 \
  -upstream http://prometheus:9090
```

The default limit may be too low for large Prometheus instances with many metrics.

### Option 2: Check Inventory Sync Logs

Look for errors in the inventory sync process:

```bash
# Look for lines like:
level=INFO msg="inventory: sync complete"
level=WARN msg="failed to query metrics for job"
```

Failed job queries won't populate the inventory.

### Option 3: Verify Backend Accessibility

Test that your metrics backend responds to metadata queries:

```bash
# Test metadata endpoint
curl "http://prometheus:9090/api/v1/metadata"

# Test label values endpoint
curl "http://prometheus:9090/api/v1/label/__name__/values"
```

If these fail, the proxy can't build the inventory.

### Option 4: Manual Sync Trigger

The inventory syncs periodically (default: every 10 minutes). To force an immediate sync, restart the proxy.

## Database Connection Issues

**Symptom:** Errors like `failed to connect to database` or `database timeout`.

### PostgreSQL Connection Issues

#### Check Connection String

Verify your PostgreSQL configuration:

```bash
./prom-analytics-proxy \
  -database-provider postgresql \
  -postgresql-addr localhost \
  -postgresql-port 5432 \
  -postgresql-database prom_analytics \
  -postgresql-user analytics \
  -postgresql-dial-timeout 10s
```

#### Test Connection

```bash
# Test PostgreSQL connection directly
psql -h localhost -p 5432 -U analytics -d prom_analytics
```

#### Check SSL Mode

If your PostgreSQL requires SSL:

```bash
./prom-analytics-proxy \
  -postgresql-sslmode require \
  ...
```

#### Verify User Permissions

The database user needs permissions to:

- Create tables (for migrations)
- Insert, update, delete data
- Create indexes

### SQLite Connection Issues

#### Check File Permissions

Ensure the SQLite database file and directory are writable:

```bash
ls -la prom-analytics-proxy.db
# Should show write permissions for your user
```

#### Check Disk Space

SQLite databases can grow large. Ensure adequate disk space:

```bash
df -h .
```

#### File Locking Issues

If running multiple instances, ensure they use different SQLite files (SQLite doesn't support concurrent writes well).

## Memory/Resource Issues

**Symptom:** Proxy consumes excessive memory or CPU.

### High Memory Usage

**Cause:** Large insert buffer - if you have a very large `insert-buffer-size`, this consumes memory.

**Solution:** Reduce buffer size and increase flush frequency:

```yaml
insert-buffer-size: 100       # Smaller buffer
insert-flush-interval: 2s     # More frequent flushes
```

**Cause:** Large inventory - tracking hundreds of thousands of metrics consumes memory.

**Solution:** Consider using PostgreSQL and ensure adequate memory allocation.

### High CPU Usage

**Cause:** Too many concurrent queries.

**Solution:** This is usually fine - the proxy is just handling traffic. If CPU is a concern:

1. Ensure you're not running unnecessary background processes
2. Use PostgreSQL instead of SQLite (more efficient writes)
3. Scale horizontally with multiple proxy instances

### OOM (Out of Memory) Kills

If the proxy is being killed by the OOM killer:

```yaml
# In Kubernetes, increase memory limits
resources:
  limits:
    memory: 2Gi
  requests:
    memory: 512Mi
```

## Getting More Help

### Enable Debug Logging

```bash
./prom-analytics-proxy \
  -log-level DEBUG \
  -log-format json \
  -upstream http://prometheus:9090
```

This provides detailed information about what the proxy is doing.

### Collect Diagnostic Information

When reporting an issue, include:

1. Proxy version: `./prom-analytics-proxy -version`
2. Configuration (sanitized, remove passwords)
3. Full logs showing the error
4. Description of your environment (Prometheus/Thanos version, number of metrics, query rate)
5. Database type and version

### Open an Issue

If you can't resolve your issue:

1. Check [existing issues](https://github.com/nicolastakashi/prom-analytics-proxy/issues)
2. Open a [new issue](https://github.com/nicolastakashi/prom-analytics-proxy/issues/new) with diagnostic information
3. Include the troubleshooting steps you've already tried

## Related Documentation

- [Quick Start Guide](quick-start.md)
- [Configuration Reference](../README.md#configuration)
- [API Reference](../README.md#api-reference)
