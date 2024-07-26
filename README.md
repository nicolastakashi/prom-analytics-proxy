# prom-analytics-proxy

## Clickhouse docker container

```bash
docker run -d --name clickhouse-server --ulimit nofile=262144:262144 -p 9000:9000 -p 8123:8123 clickhouse/clickhouse-server:latest
```