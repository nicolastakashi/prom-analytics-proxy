# syntax=docker/dockerfile:1

FROM golang:1.23.0 AS build

WORKDIR /go/src/github.com/MichaHoffmann/prom-analytics-proxy

RUN apt-get update
RUN useradd -ms /bin/bash cole

COPY --chown=cole:cole . .

RUN make all

FROM gcr.io/distroless/static:latest-amd64

WORKDIR /prom-analytics-proxy

COPY --from=build /go/src/github.com/MichaHoffmann/prom-analytics-proxy/bin/* /bin/

USER nobody

ENTRYPOINT [ "/bin/prom-analytics-proxy" ]