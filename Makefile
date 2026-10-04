# Every target here is safe to run on a fresh clone after `make install`.

install:
	go mod tidy

# The server binary embeds static/config.yaml, which is gitignored, so it has to
# exist before anything compiles. The example is a valid default config.
config:
	@test -f static/config.yaml || cp static/config.example.yaml static/config.yaml

run: config
	go run ./cmd/server

# The queue consumers, as a separate process.
worker: config
	go run ./cmd/rabbit

build: config
	go build ./...

# Mint the one time token a new account registers with.
genregtoken: config
	go run ./cmd/admin/genregtoken -issued-by $(or $(ISSUED_BY),cli)

test: config
	go test ./...

test-coverage: config
	go test -coverprofile=coverage.out ./...
	go tool cover -html=coverage.out

vet:
	go vet ./...

# Install with: go install github.com/cespare/reflex@latest
watch: config
	ulimit -n 1000
	reflex -s -r '\.go$$' make run

.PHONY: install config run worker build genregtoken test test-coverage vet watch
