
# Kubernetes YAML manifests

This folder contains an opinionated set of Kubernetes YAML manifests to help you get started exploring prom-analytics-proxy.

## Please Note

- These manifests are not intended for production.
- For the sake of exploration, keep prom-analytics-proxy and PostgreSQL in the same Namespace

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
