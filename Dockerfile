# syntax=docker/dockerfile:1

FROM node:23.6.0-alpine AS uibuild

WORKDIR /app/ui

COPY ui/package.json ui/package-lock.json ./
RUN --mount=type=cache,target=/root/.npm npm ci --legacy-peer-deps

COPY ui/ ./
RUN NODE_ENV=production npm run build

FROM golang:1.26 AS gobuild

WORKDIR /app

RUN useradd -ms /bin/bash prom-analytics-proxy

ARG REVISION=unknown
ARG BRANCH=unknown
ARG RELEASE_VERSION=unknown

COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod go mod download

COPY --from=uibuild /app/ui/dist ./ui/dist
COPY --chown=prom-analytics-proxy:prom-analytics-proxy . .

RUN --mount=type=cache,target=/root/.cache/go-build \
    make build REVISION=$REVISION BRANCH=$BRANCH RELEASE_VERSION=$RELEASE_VERSION

FROM gcr.io/distroless/static:latest AS final-distroless

WORKDIR /prom-analytics-proxy

COPY --from=gobuild /app/bin/* /bin/

USER nobody

ENTRYPOINT [ "/bin/prom-analytics-proxy" ]

FROM alpine:3.19 AS final-alpine

WORKDIR /prom-analytics-proxy

COPY --from=gobuild /app/bin/* /bin/

USER nobody

ENTRYPOINT [ "/bin/prom-analytics-proxy" ]
