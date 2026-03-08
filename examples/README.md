# Examples

Two demos are available:

## Proxy Only

Path: `examples/proxy`

Use this when you want to show only the query analytics story:
- Prometheus as backend
- `prom-analytics-proxy` as the query proxy
- Perses dashboards through the proxy
- metrics-usage inventory and rule usage

Start it with:

```bash
cd examples/proxy
docker compose up -d
```

## Full Demo

Path: `examples/full`

Use this when you want the full story:
- query analytics through the proxy API
- OTLP scraping through OTel Collector
- ingester dry-run and drop analysis
- metrics forwarded into Prometheus without Prometheus scraping targets directly

Start it with:

```bash
cd examples/full
docker compose up -d
```

## Load Testing

Generate query traffic against either demo:

```bash
cd examples/promqlsmith
go run main.go -url http://localhost:9091 -duration 5m -workers 2
```
