# Build stage: compile every binary in ./cmd.
FROM golang:1.27 AS build

WORKDIR /src

# Dependencies first, so a change to the source does not re-download the module
# cache on every build.
COPY go.mod go.sum ./
RUN go mod download

COPY . .

# static/config.yaml is embedded at compile time and gitignored, so it is
# deliberately absent from the build context. The example stands in for it here,
# the same way `make build` handles a fresh clone. Bake in your own by replacing
# this line, or by building with a config.yaml present in the context.
RUN cp static/config.example.yaml static/config.yaml

RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/server      ./cmd/server && \
    CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/worker      ./cmd/rabbit && \
    CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/genregtoken ./cmd/admin/genregtoken

# Runtime stage: no toolchain, no source.
FROM alpine:3.20

# ca-certificates for outbound TLS, tzdata so timestamps are not stuck on UTC.
RUN apk add --no-cache ca-certificates tzdata && \
    adduser -D -u 10001 app

COPY --from=build /out/server      /usr/local/bin/server
COPY --from=build /out/worker      /usr/local/bin/worker
COPY --from=build /out/genregtoken /usr/local/bin/genregtoken

USER app
EXPOSE 8080

ENTRYPOINT ["/usr/local/bin/server"]