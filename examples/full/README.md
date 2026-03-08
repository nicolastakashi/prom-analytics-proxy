# Full Demo

This demo includes both product flows:
- query analytics through `prom-analytics-proxy`
- OTLP ingestion and dry-run filtering through `prom-analytics-proxy ingester`

## Start

```bash
docker compose up -d
```

## Services

- `http://localhost:9090` - Prometheus backend
- `http://localhost:9091` - prom-analytics-proxy UI and API
- `http://localhost:8080` - Perses
- `http://localhost:8081` - metrics-usage
- `http://localhost:9100/metrics` - node exporter
- `http://localhost:4320/metrics` - ingester metrics

## Flow

```text
Node -> OTel Collector -> prom-analytics-proxy ingester -> OTel Collector -> Prometheus
Perses -> prom-analytics-proxy API -> Prometheus
```

## Dry Run

The ingester starts in dry-run mode. To enable actual dropping, edit `docker-compose.yaml` and change:

```yaml
- --ingester-dry-run=true
```

to:

```yaml
- --ingester-dry-run=false
```

Then recreate the ingester:

```bash
docker compose up -d --force-recreate prom-analytics-proxy-ingester
```
