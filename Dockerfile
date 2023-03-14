FROM golang:1.20 AS build
COPY / /src
WORKDIR /src
RUN --mount=type=cache,target=/go/pkg --mount=type=cache,target=/root/.cache/go-build make build

FROM alpine:3.17 AS base
RUN apk add --no-cache ca-certificates curl
RUN adduser -D acorn
USER acorn
ENTRYPOINT ["/usr/local/bin/istio-plugin"]
COPY --from=build /src/bin/istio-plugin /usr/local/bin
