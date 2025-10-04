# Quick Start Guide

This guide will walk you through setting up `prom-analytics-proxy` and configuring your query clients to use it.

## Prerequisites

- A running Prometheus, Thanos, or Cortex instance
- Access to configure your query clients (Grafana, Perses, etc.)
- Go 1.21+ (if building from source)

## Step 1: Start the Proxy

### Option A: Build from Source

```bash
# Clone the repository
git clone https://github.com/nicolastakashi/prom-analytics-proxy.git
cd prom-analytics-proxy

# Build and run
make build
./prom-analytics-proxy -upstream http://your-prometheus-server:9090
```

### Option B: Using Docker

```bash
docker run -p 9091:9091 \
  ghcr.io/nicolastakashi/prom-analytics-proxy:latest \
  -upstream http://your-prometheus-server:9090
```

### Option C: Using Docker Compose (Recommended for Testing)

The easiest way to try out the proxy is using the provided Docker Compose setup:

```bash
cd examples
docker compose up -d
```

This will start:

- **Prometheus** (port 9090) - with sample rules and configuration
- **PostgreSQL** (port 5432) - database backend for the proxy
- **prom-analytics-proxy** (port 9091) - the proxy itself
- **Perses** (port 8080) - dashboard tool with pre-configured dashboards
- **Metrics Usage** (port 8081) - metrics usage tracking UI

See the [examples/docker-compose.yaml](../examples/docker-compose.yaml) and [examples/config/](../examples/config/) for the full configuration.

The proxy will start on port `:9091` by default.

## Step 2: Reconfigure Your Query Clients

**This is the critical step!** You must update your query clients to send queries to the proxy instead of directly to Prometheus.

### For Grafana

1. Go to **Configuration → Data Sources**
2. Edit your Prometheus data source
3. Change the URL from `http://prometheus:9090` to `http://prom-analytics-proxy:9091`
4. Click **Save & Test**

### For Perses

Update your datasource configuration to point to the proxy:

```yaml
datasources:
  - name: PrometheusDemo
    default: true
    plugin:
      kind: PrometheusDatasource
      spec:
        proxy:
          kind: HTTPProxy
          spec:
            url: http://prom-analytics-proxy:9091  # Changed from prometheus:9090
```

### For Custom Applications

Update your Prometheus client configuration to use the proxy URL:

```go
// Before
prometheusURL := "http://prometheus:9090"

// After
prometheusURL := "http://prom-analytics-proxy:9091"
```

### For Kubernetes Deployments

If your applications use a Kubernetes Service to reach Prometheus, you can:

**Option 1: Update the Service selector** to point to the proxy instead of Prometheus

**Option 2: Deploy the proxy as a sidecar** alongside your application

**Option 3: Create a new Service** for the proxy and update your applications to use it

## Step 3: Verify Data Collection

1. Open the web UI at `http://localhost:9091` (or your proxy address)
2. Execute some queries from your clients (open Grafana dashboards, etc.)
3. Refresh the UI - you should see captured query analytics appear

### Verification Checklist

- [ ] Proxy is running and accessible
- [ ] Query clients are configured to use the proxy URL (`:9091`)
- [ ] Test queries work through the proxy
- [ ] Analytics data appears in the web UI
- [ ] Proxy logs show query traffic

If you don't see any data, verify that your clients are actually sending queries to the proxy (check the logs). See the [Troubleshooting Guide](troubleshooting.md) for common issues.

## Next Steps

- [Configure the database backend](../README.md#database-configuration) (PostgreSQL or SQLite)
- [Tune performance settings](../README.md#performance-tuning) for your workload
- [Configure inventory sync](../README.md#inventory-configuration) for metrics discovery
- [Set up tracing](../README.md#tracing-support) (optional)
- Explore the [API Reference](../README.md#api-reference) for programmatic access

## Examples and Configuration

### Ready-to-Use Examples

The [`examples/`](../examples/) directory contains complete working configurations:

#### Docker Compose Setup

[`examples/docker-compose.yaml`](../examples/docker-compose.yaml) - Complete stack with:

- Prometheus with alerting rules
- PostgreSQL database
- prom-analytics-proxy configured to use PostgreSQL
- Perses with sample dashboards
- Metrics Usage integration

#### Configuration Examples

- [`examples/config/prometheus/`](../examples/config/prometheus/) - Prometheus configuration with rules
- [`examples/config/perses/`](../examples/config/perses/) - Perses configuration with sample dashboards
- [`examples/config/metrics-usage/`](../examples/config/metrics-usage/) - Metrics Usage integration config

### Command Line Examples

#### Minimal Configuration (SQLite)

```bash
./prom-analytics-proxy \
  -upstream http://prometheus:9090 \
  -database-provider sqlite \
  -sqlite-database-path ./data/analytics.db
```

#### Production Configuration (PostgreSQL)

```bash
./prom-analytics-proxy \
  -upstream http://prometheus:9090 \
  -database-provider postgresql \
  -postgresql-addr postgres.example.com \
  -postgresql-port 5432 \
  -postgresql-database prom_analytics \
  -postgresql-user analytics \
  -insert-batch-size 50 \
  -insert-buffer-size 500
```

#### Using a Configuration File

Create a `config.yaml` file:

```yaml
upstream: "http://prometheus:9090"
insecure-listen-address: ":9091"
database-provider: "postgresql"

postgresql-addr: "postgres.example.com"
postgresql-port: 5432
postgresql-database: "prom_analytics"
postgresql-user: "analytics"
postgresql-password: "your-password"

insert-batch-size: 50
insert-buffer-size: 500
insert-flush-interval: "10s"

inventory:
  enabled: true
  sync_interval: 15m
  time_window: 720h
```

Then run:

```bash
./prom-analytics-proxy -config-file config.yaml
```

## Common Setup Patterns

### Pattern 1: Local Development

```bash
# Run Prometheus locally
docker run -p 9090:9090 prom/prometheus

# Run the proxy pointing to it
./prom-analytics-proxy -upstream http://localhost:9090

# Configure Grafana to use http://localhost:9091
```

### Pattern 2: Kubernetes Sidecar

Deploy the proxy as a sidecar container alongside your application, so all Prometheus queries from that app go through the proxy automatically.

### Pattern 3: Central Proxy

Deploy a single proxy instance that all your query clients (Grafana, Perses, custom apps) connect to. This centralizes analytics collection.

### Pattern 4: Per-Team Proxies

Deploy separate proxy instances for different teams, each with their own database. This provides team-specific analytics and isolation.

## Getting Help

- Check the [Troubleshooting Guide](troubleshooting.md)
- Review the [Configuration Reference](../README.md#configuration)
- Open an issue on [GitHub](https://github.com/nicolastakashi/prom-analytics-proxy/issues)
