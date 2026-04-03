
# Kubernetes YAML manifests

This folder contains an opinionated set of Kubernetes YAML manifests to help you get started exploring prom-analytics-proxy.

## Please Note

- These manifests are not intended for production
- PostgreSQL manifests are not intended for production, they are intended as a more production-oriented example
- Prometheus manifests are not provided to avoid any bias, we advise to deploy it using the [well known K8s operator](https://github.com/prometheus-operator/prometheus-operator)
- For the sake of exploration, we keep both prom-analytics-proxy and PostgreSQL in the same K8s Namespace

## Deploy

```shell
NAMESPACE=#...
kubectl apply -n ${NAMESPACE} -f postgresql/
kubectl apply -n ${NAMESPACE} -f prom-analytics-proxy/
```

## Remove

```shell
NAMESPACE=#...
kubectl delete -n ${NAMESPACE} -f postgresql/
kubectl delete -n ${NAMESPACE} -f prom-analytics-proxy/
```
