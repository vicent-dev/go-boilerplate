# AGENTS.md

Orientation for this repository. Read this before changing anything: most of the
decisions that look arbitrary below are deliberate, and the reasoning is recorded
here so it is not undone by accident.

## What this is

A starting point for a Go HTTP service with the boring parts already done:
authentication, PostgreSQL persistence, a message queue, configuration, graceful
shutdown and a test suite. A project clones it and writes domain logic.

The module path is `go-boilerplate`, with no dot in it. That is intentional — the
scaffolding script rewrites it with `sed` when a project is created — but it also
means the module cannot be `go get`-ed from a remote, so the Dockerfile must not
try.

## Setup

`static/config.yaml` is **embedded at compile time** (`static/embed.go`) and
gitignored, so a fresh clone does not compile until it exists:

```bash
make config      # copies static/config.example.yaml to static/config.yaml
```

`make run`, `make build`, `make test` and `make worker` all depend on that target,
so they create it for you. Only a bare `go build ./...` needs the copy done by
hand.

Values in the config support `${VAR}` and `${VAR:-default}`, resolved from the
process environment plus an optional `.env` file loaded at startup. See
`app/config.go` (`LoadConfig` and `expandEnv`); `os.ExpandEnv` cannot be used
because it would read `VAR:-default` as a variable name.

**The config is parsed with `gopkg.in/yaml.v3`, and it has to stay that way.** Only
v3 turns `access_ttl: 15m` into a `time.Duration`; yaml.v2 silently yields zero,
which issues access tokens that expire the moment they are minted.
`TestYAMLParsesDurations` in `app/config_test.go` pins this.

## Commands

```bash
make install      # go mod tidy
make config       # create static/config.yaml from the example
make run          # the http api
make worker       # the queue consumers, as a separate process
make genregtoken  # mint a one time registration token
make build        # compile everything
make test         # go test ./...
make test-coverage
make vet
make watch        # reflex hot reload (go install github.com/cespare/reflex@latest)

docker compose up --build          # api + worker + postgres + rabbit
docker compose run --rm api        # one api container
docker compose up --scale worker=3 # more consumers
```

CI runs `make build`, `make test` and `make vet` on every push and PR against
`main` (`.github/workflows/go.yml`), copying the example config first. There is no
linter config yet.

## Layout

| Path              | Role                                                              |
| ----------------- | ----------------------------------------------------------------- |
| `cmd/server/`     | HTTP API entrypoint                                               |
| `cmd/rabbit/`     | Queue worker entrypoint; no listener, no http stack               |
| `cmd/admin/`      | One-shot operational commands (`genregtoken`)                     |
| `app/`            | Wiring only: config, database, routes, middleware, worker startup   |
| `pkg/auth/`       | Accounts, sessions, JWT issuance and verification, bearer middleware |
| `pkg/repo/`       | Generic persistence port + GORM implementation                     |
| `pkg/queue/`      | Broker port + RabbitMQ implementation, manual ack, per-queue channels |
| `pkg/log/`        | `alog` wrappers that read method and path out of `context.Context` |
| `pkg/util/`       | Small helpers with no dependencies                                  |
| `static/`         | Embedded YAML config                                               |
| `docker/`         | Two-stage build → `/usr/local/bin/{server,worker,genregtoken}`      |

## Request flow

1. `loggingMiddleware` stamps the method and path onto the context, so every log
   line written downstream can name its request, and emits the access log after
   the handler returns.
2. `GET /ping` answers publicly and needs neither a token nor the database. It
   publishes to the `ping` queue when one is configured, and **logs and carries on
   if the broker is down**: a liveness probe that fails because a queue is down
   would take a healthy api out of rotation.
3. `/auth/{register,login,logout,refresh}` are public — they are how a caller gets
   a token in the first place — and are rate limited per client IP.
4. Everything under `/api` sits behind `authMiddleware`, which injects a
   `*auth.Principal` into the request context. `registerDomainRoutes` in
   `app/route.go` is where a project adds its own handlers; they arrive already
   authenticated.

## Adding a domain

1. Write the use case in `pkg/<domain>/`, with a `Store` port and its sentinel
   `errors.go`. Do not import `app/`.
2. Add a `<domain>_handlers.go` in `app/`, following `auth_handlers.go`: decode,
   delegate, render. One `write<Domain>Error` maps the sentinels to statuses.
3. Register the handlers on the `api` subrouter in `registerDomainRoutes`.
4. For a worker, add a handler in `newConsumerHandlers` in `app/queue.go`.
5. Add a queue to the config if the domain needs one, and a handler to match — the
   worker refuses to start on a declared queue with no handler, on purpose.

## Error handling

The pattern `pkg/auth` established, which the rest of the codebase follows:

- Each domain owns an `errors.go` of package-prefixed sentinels (`auth: …`,
  `repo: …`, `queue: …`). Sentinels are documented with their *intent*, not just
  their name — `auth.ErrInvalidCredentials` covers an unknown email and a wrong
  password on purpose, so login cannot enumerate accounts.
- **No package under `pkg/` decides a status code or renders an error.** HTTP
  *types* do appear in an adapter — `auth.RequireAuth` is a middleware and
  `auth.BearerToken` reads an `http.Header` — but how a rejection looks is always
  injected or left to `app/`. That is what `auth.RequireAuth`'s `onError
  ErrorHandler` port is for: the domain can reject a request without knowing how.
- Detail travels with the sentinel rather than replacing it:
  `fmt.Errorf("%w: email is required", ErrInvalidInput)`. Callers classify with
  `errors.Is`.
- Foreign errors are collapsed at the boundary so no caller imports the driver:
  `auth.parseError` maps jwt failures to `ErrInvalidToken`/`ErrTokenExpired`,
  `repo.NormalizeError` maps `gorm.ErrRecordNotFound` to `repo.ErrNotFound` and a
  unique index collision to `repo.ErrDuplicate`, and `pkg/queue` wraps every amqp
  failure in one of its own sentinels.
- A generic CRUD method must normalize too. `GormRepository.Create` and `Update`
  returned the raw driver error for years because only the lookups were wrapped,
  and the domain above them had no way to recognise a duplicate.
- Exactly one mapping point per domain lives in `app/`: `writeAuthError` switches
  on `errors.Is`, and its `default` branch logs the real error and answers
  `500 {"error": "internal error"}`.
- Client-visible messages are fixed per failure class and never `err.Error()`. The
  queue errors carry broker internals, so echoing them would leak them.
- Anything a caller is expected to handle as a normal outcome is a sentinel rather
  than a failure: `repo.ErrNotFound` is how a lookup reports nothing.
- Inject whatever a test needs to control: `auth.Authenticator`, `auth.Store`,
  `Service.now`, `Rabbit`'s dial function, `ipRateLimiter.now`.

## Conventions

- Config and route wiring live in `app/`; reusable logic lives in `pkg/` and must
  not import `app/`.
- Handlers are returned as closures from `s.someHandler()` methods so per-route
  dependencies are captured at registration time.
- Use the `log.Log*` helpers rather than the stdlib `log` or `fmt.Println`, so
  entries pick up method and path context. Never hand them
  `context.Background()` when a request context exists — the method and path that
  context carries are the point.
- Ports are declared in the domain and implemented next to them. `amqp091-go`
  exposes `Connection` and `Channel` as **structs, not interfaces**, which is why
  `pkg/queue` declares `amqpChannel`/`amqpConn` itself: that is what lets the
  package be tested without a broker. The same trick applies to any driver you add.
- Never hand a body to two owners. Read it once and pass the bytes on.
- The two binaries are separate on purpose. A worker scales differently from a web
  server and must not need a listening port.

## Queue semantics

- **Acknowledgement is manual.** `Consume` asks for `autoAck=false` and acks only
  after the handler returns nil. A handler that returns an error has its message
  requeued, and the failure is reported through `Rabbit.OnError` rather than
  logged by the package.
- That means a handler which always fails requeues its message forever. Anything
  that can fail permanently needs a bounded retry of its own (see below).
- Publishing uses a pooled channel, so a burst of messages does not open a channel
  per message. A channel the broker closed is never handed back out.
- `QueueConfig.Exchange` is the **routing key** used when publishing. Exchanges
  are not declared.
- A queue declared with no handler makes the worker refuse to start.

## Known issues / TODOs

Deliberately not fixed, because fixing them means changing behaviour or adding
features. Each is a decision to revisit, not an oversight:

- **A consumer that always fails spins.** A requeued message comes straight back,
  so a poison payload becomes a hot loop that starves the queue. The fix is a retry
  counter in the handler, or a dead-letter queue; neither is implemented.
- **No broker reconnection.** If the connection drops, operations return
  `ErrNotConnected` and the process has to be restarted by whatever supervises it.
  Compose's `restart: unless-stopped` is the intended answer for now.
- **The rate limiter trusts `RemoteAddr`.** `clientIP` deliberately ignores
  `X-Forwarded-For`, because a limiter keyed on a client-supplied header is
  defeated by sending a different one each request. Behind a proxy, either
  configure the proxy to set a trusted header and change `clientIP`, or accept that
  every caller shares one bucket.
- **Rate limit buckets are in memory.** They are per process, so N replicas give N
  times the configured rate. Sharing them needs Redis, which this repository does
  not depend on.
- **gorilla/mux answers 404, not 405, for a method mismatch on a subrouter.**
  v1.8.1 loses `ErrMethodMismatch` when a subrouter holds more than one route:
  with `MethodNotAllowedHandler` set, `/auth/refresh` returns 405 while
  `/auth/login` returns 404 for the same class of mistake. Half-supporting 405 is
  worse than not supporting it, so no `MethodNotAllowedHandler` is registered and
  the test documents the real behaviour.
- **Migrations are `AutoMigrate` at startup.** Fine for a boilerplate; a project
  with real schema history wants versioned migrations and a separate command.
- **`OpenDB` migrates on every start**, including from the `genregtoken` command.
- **No token revocation on access tokens.** Logout revokes the session, but an
  access token stays valid until it expires, which is up to `access_ttl`. Shorter
  TTLs or a denylist are the two ways out.
- **No email verification, password reset or account lockout.** `Role` and
  `IsActive` exist on the model, and nothing reads them for anything else yet.
- **Only postgres is really supported.** The suite runs against SQLite, because
  it needs no server, so the two can disagree. Anything dialect sensitive — and
  the unique index handling below was not — needs a check against postgres.

Already fixed (kept here so the reasoning is not lost):

- Auth was a stub that only checked the `Authorization` header was non-empty. It is
  now a real domain: GORM store, bcrypt, a JWT issuer with separate access and
  refresh secrets, refresh rotation with reuse detection.
- The rabbit code discarded every error, `RunConsumers` waited on a `WaitGroup`
  whose `Done` was never called, `produce` re-declared the queue on every message,
  and consumers acked on delivery so a failed handler lost the message silently.
  All of that is gone: see Queue semantics above.
- The database was MySQL with the driver discarded on error; it is PostgreSQL with
  errors returned, a sized pool and a `PingContext`.
- `static/config.yaml` was gitignored *and* `go.sum` was gitignored, so a fresh
  clone could not build. `go.sum` is committed now.
- The Dockerfile ran `go get go-boilerplate`, which cannot work for a module path
  without a dot in it. Removed, and the image is two-stage.
- `docker-compose.yaml` gated the api on a healthcheck that did not exist, and did
  not set `DB_HOST`, so a containerised api dialled its own loopback.
- **The api could only ever have one user.** `User.Username` carried a
  `uniqueIndex` and was optional, so every account that omitted it stored `''` and
  the index admitted exactly one such row; the second registration died on
  `23505` and surfaced as a 500. It is now `*string`, so an omitted alias is SQL
  NULL, which no unique index counts twice. `TestManyUsersCanLeaveTheUsernameUnset`
  in `pkg/auth/store_gorm_test.go` pins it.
- **A duplicate email could surface as a 500.** `Register` checks `ByEmail` first,
  which is not a lock, so a concurrent pair both pass it and collide on the
  index. `repo.ErrDuplicate` now recognises that, and `Service.Register` maps it
  to `ErrEmailTaken` so the race answers 409 like every other duplicate.
  `GormRepository.Create` and `Update` were returning the raw driver error because
  they skipped `NormalizeError`.
- **`NormalizeError(nil)` panicked** on `err.Error()`. It returns nil now.
- **The worker's entrypoint was invisible to git.** The `rabbit` line in
  `.gitignore`, meant for the compiled binary, also matched the `cmd/rabbit/`
  *directory*, so `main.go` was silently excluded from every commit. Binary rules
  are anchored with a leading slash now; check `git check-ignore` after touching
  either ignore file.
- **The Docker build could not have worked.** `.dockerignore` excludes
  `static/config.yaml` on purpose, and the embed needs it, so the image failed to
  compile. The Dockerfile copies the example in, exactly as `make config` does.
- **`container_name` on the worker blocked scaling.** The compose comment
  advertised `--scale worker=3`, which a fixed name makes impossible.
- **Every log line outside a request opened with `<nil> - <nil>:`,** which is
  every line a worker writes, since a worker has no request to name. `pkg/log`
  drops the prefix when there is no request.