DB ?= $(shell scripts/devdb.sh url)

.PHONY: build test db eval bench demo wasm web clean

build:
	go build -o bin/ ./cmd/...

# Unit tests, plus the server tests against the dev Postgres when it runs.
test:
	go vet ./...
	WARRANT_TEST_DB=$(DB) go test -race ./...

db:
	scripts/devdb.sh start

eval: build
	bin/warrant eval -db $(DB) -out results/eval.json

bench: build
	bin/warrant bench -db $(DB) -out results/bench.json

demo: build
	bin/warrant demo -db $(DB) -pack web/public/sample-pack.json -transcript web/src/data/story.json

# The browser verifier: evidence.Verify compiled to WebAssembly.
wasm:
	GOOS=js GOARCH=wasm go build -trimpath -ldflags="-s -w" -o web/public/verify.wasm ./cmd/verify-wasm
	install -m 0644 "$$(go env GOROOT)/lib/wasm/wasm_exec.js" web/public/wasm_exec.js

web: wasm
	cd web && pnpm install && pnpm build

clean:
	rm -rf bin dist web/out web/.next
