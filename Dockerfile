# syntax=docker/dockerfile:1

FROM node:22.8.0-alpine AS uibuild

WORKDIR /go/src/github.com/MichaHoffmann/prom-analytics-proxy

COPY ./ui .

RUN npm install
RUN npm run build

FROM golang:1.23.0 AS gobuild

WORKDIR /go/src/github.com/MichaHoffmann/prom-analytics-proxy

RUN apt-get update
RUN useradd -ms /bin/bash prom-analytics-proxy

COPY --from=uibuild  /go/src/github.com/MichaHoffmann/prom-analytics-proxy/dist ./ui/dist

RUN ls -la

COPY --chown=prom-analytics-proxy:prom-analytics-proxy . .

RUN make all

FROM gcr.io/distroless/static:latest-arm64

WORKDIR /prom-analytics-proxy

COPY --from=gobuild /go/src/github.com/MichaHoffmann/prom-analytics-proxy/bin/* /bin/

USER nobody

ENTRYPOINT [ "/bin/prom-analytics-proxy" ]