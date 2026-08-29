# The Go binary embeds the built frontend (cmd/server/web/dist, gitignored as a
# build artifact), so `go build` cannot work on a fresh clone until the
# frontend has been built. Always come in through here.

.PHONY: frontend build test clean

frontend:
	pnpm -C web install
	pnpm -C web build

build: frontend
	go build ./cmd/server

test: frontend
	go test ./...

clean:
	rm -rf cmd/server/web/dist web/node_modules
