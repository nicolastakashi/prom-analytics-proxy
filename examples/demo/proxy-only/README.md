# Proxy Demo

This demo focuses only on query analytics through `prom-analytics-proxy`, with Prometheus exposed behind nginx.

## Start

```bash
docker compose up -d
```

## Services

- `http://localhost:9090` - nginx in front of Prometheus
- `http://localhost:9092` - Prometheus backend
- `http://localhost:9091` - prom-analytics-proxy UI and API
- `http://localhost:8080` - Perses
- `http://localhost:8081` - metrics-usage

## Flow

```text
Perses -> prom-analytics-proxy -> nginx -> Prometheus
```

Use this demo when you want to show:
- query capture
- slow query visibility
- dashboard and rule usage
- metric usage inventory
- proxy compatibility with a Prometheus backend behind nginx
