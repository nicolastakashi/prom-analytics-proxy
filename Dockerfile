# syntax=docker/dockerfile:1

FROM node:22.15.1-alpine AS uibuild

WORKDIR /go/src/github.com/nicolastakashi/prom-analytics-proxy

COPY ./ui .

RUN npm install
RUN NODE_ENV=production npm run build

FROM golang:1.24.3 AS gobuild

WORKDIR /go/src/github.com/nicolastakashi/prom-analytics-proxy

RUN apt-get update
RUN useradd -ms /bin/bash prom-analytics-proxy

COPY --from=uibuild /go/src/github.com/nicolastakashi/prom-analytics-proxy/dist ./ui/dist

COPY --chown=prom-analytics-proxy:prom-analytics-proxy . .

RUN make all

FROM gcr.io/distroless/static:latest

WORKDIR /prom-analytics-proxy

COPY --from=gobuild /go/src/github.com/nicolastakashi/prom-analytics-proxy/bin/* /bin/

USER nobody

ENTRYPOINT [ "/bin/prom-analytics-proxy" ]