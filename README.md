# go-boilerplate

A Go HTTP service with the parts every project ends up writing anyway: JWT
authentication, PostgreSQL through GORM, a RabbitMQ worker, configuration from
a file or the environment, graceful shutdown, and tests for all of it.

Clone it, rename it, and write your domain.

## Quick start

```bash
make install    # go mod tidy
make run        # http api on :8080
make test       # the whole suite
```

`make run` creates `static/config.yaml` from `static/config.example.yaml` if it
is missing. That file is **embedded into the binaries at compile time** and
gitignored, so a fresh clone cannot build without it — that is why every `make`
target that compiles depends on the `config` target.

With Docker, which brings up postgres and rabbitmq as well:

```bash
docker compose up --build          # api + worker + postgres + rabbit
docker compose up --scale worker=3 # more consumers
```

## Layout

| Path          | What lives there                                                    |
| ------------- | ------------------------------------------------------------------- |
| `cmd/server`  | HTTP API entrypoint                                                 |
| `cmd/rabbit`  | Queue worker entrypoint                                             |
| `cmd/admin`   | One-shot operational commands (`genregtoken`)                       |
| `app/`        | Wiring only: config, database, routes, middleware, worker startup   |
| `pkg/auth`    | Accounts, sessions, JWT issuance and verification                   |
| `pkg/repo`    | Generic persistence port plus its GORM implementation               |
| `pkg/queue`   | Broker port plus its RabbitMQ implementation                        |
| `pkg/log`     | Request-aware logging helpers                                       |
| `static/`     | The embedded YAML config                                            |

`app/` depends on `pkg/` and never the other way round, so a domain can be moved
out and reused without dragging the HTTP layer with it.

## Configuration

`static/config.example.yaml` documents every value. Anything in it can be
overridden from the environment, with or without a default:

```yaml
db:
  host: ${DB_HOST:-localhost}
  port: ${DB_PORT:-5432}
rabbit:
  uri: ${RABBIT_URI:-amqp://guest:guest@localhost:5672/}
```

A `.env` file in the working directory is loaded at startup. Both `${VAR}` and
`${VAR:-default}` are supported — `os.ExpandEnv` cannot do the latter, which is
why `app/config.go` has its own expansion.

Durations are written the Go way (`15m`, `24h`), which is why the config is
parsed with `gopkg.in/yaml.v3`. Do not downgrade that dependency: v2 silently
parses `15m` as zero, and a zero access TTL issues tokens that expire on arrival.

## The API

```
GET  /ping                          public, no database, publishes to `ping`
POST /auth/register                 requires a registration token
POST /auth/login
POST /auth/refresh                  rotates the refresh token
POST /auth/logout                   revokes the session
GET  /api/ping                      authenticated
GET  /api/me                        authenticated, returns the account
```

`/auth/*` is rate limited per client IP. `/api/*` sits behind
`authMiddleware`, which puts an `*auth.Principal` in the request context.

`/ping` deliberately keeps answering when the broker is down. A liveness probe
that fails because rabbitmq is restarting would pull a healthy api out of
rotation for the wrong reason.

## Writing your own endpoints

Add a domain package under `pkg/`, then register handlers in
`registerDomainRoutes` in `app/route.go`. They arrive already authenticated:

```go
func registerDomainRoutes(api *mux.Router) {
    // api is behind authMiddleware, so r.Context() carries *auth.Principal
    api.HandleFunc("/widgets", s.widgetHandlers().list).Methods(http.MethodGet)
}
```

For queue work, add a handler to `newConsumerHandlers` in `app/queue.go` and
declare the queue in the config. A declared queue with no handler stops the
worker from starting, so a typo is a startup error rather than a message nobody
is reading.

## Authentication

Access and refresh tokens are separate JWTs, signed with separate secrets, so
leaking one does not let an attacker mint the other.

- Passwords are bcrypt hashed.
- A `username` is an optional alias. Leave it out and it is stored as SQL NULL,
  so any number of accounts may do so; set it and it has to be unique.
- Refresh tokens rotate on use. Reusing one that was already spent revokes the
  whole token family, which is how a stolen refresh token gets caught.
- Logout revokes the session. It does **not** revoke an already-issued access
  token; those stay valid until `access_ttl` expires. Shorten the TTL or add a
  denylist if that matters to you.

Accounts register with a one-time token rather than an open signup:

```bash
make genregtoken                                   # uses auth.registration_token_ttl
go run ./cmd/admin/genregtoken -ttl 30m -issued-by support
```

## Queue behaviour worth knowing

Acknowledgement is manual. `Consume` passes `autoAck=false` and acks only after
the handler returns nil; a handler that returns an error has its message
requeued and the error reported through `Rabbit.OnError`.

That has one sharp edge: a handler which always fails requeues its message
immediately and forever, which spins. If part of your work can fail
permanently, add a retry count to your payload and give up after a few attempts,
or declare a dead-letter queue. The boilerplate does not pick that policy for
you.

`QueueConfig.Exchange` is used as the routing key when publishing. No exchange
is declared — this assumes the default one.

## Testing

```bash
make test           # go test ./...
make test-coverage
go test -race ./...
```

Nothing in the suite needs postgres, rabbitmq or a network. The auth store runs
against in-memory SQLite, and `pkg/queue` declares its own `amqpChannel` and
`amqpConn` interfaces because `amqp091-go` ships `Connection` and `Channel` as
structs rather than interfaces; that is what makes the RabbitMQ code testable
without a broker.

## Known limitations

Deliberate, and worth reading before you build on this:

- **No broker reconnection.** If the connection drops, operations fail with
  `queue: not connected` and the process needs restarting. Compose's
  `restart: unless-stopped` is the intended answer for now.
- **A failing consumer requeues in a hot loop.** See above; add bounded retries.
- **Rate limit buckets are in memory,** so N replicas allow N times the
  configured rate. Sharing them needs Redis.
- **The rate limiter trusts `RemoteAddr`** and ignores `X-Forwarded-For`,
  because a limiter keyed on a client-supplied header is defeated by sending a
  different one each request. Behind a proxy, configure the proxy to set a
  header you trust and change `clientIP`, or accept that every caller shares one
  bucket.
- **Migrations are `AutoMigrate` on startup.** Fine here; a project with real
  schema history wants versioned migrations and a separate command.
- **A method mismatch can answer 404 instead of 405** on the multi-route
  `/auth` subrouter. That is a gorilla/mux v1.8.1 limitation, not a choice;
  `app/route_test.go` pins the actual behaviour.
- **No email verification, password reset or account lockout.** `Role` and
  `IsActive` exist on the user model and nothing reads them yet.

## Scaffolding a project

The repository doubles as its own generator:

```bash
./go-boilerplate my-service    # run from a parent directory
```

It clones the repository, rewrites `go-boilerplate` to your module path
everywhere (and the bare `boilerplate` too, which covers compose container names
and the default token issuer), drops the boilerplate's own `.git` history and
scaffolding, and starts a fresh repository. `./install.sh` does the same over
SSH.

The module path has no dot in it, which is fine for a template — the generator
rewrites it — but it does mean the module cannot be fetched from a remote, so
Docker builds copy the source in rather than running `go get`.

## CI

`.github/workflows/go.yml` runs `make build`, `make test` and `make vet` on every
push and pull request against `main`. There is no linter config yet.

## License

MIT. See [LICENSE](LICENSE).
