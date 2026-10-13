VERSION ?= 0.1.0
LDFLAGS := -s -w -X github.com/fadhlanhawali/grepalert/internal/agent.Version=$(VERSION)

.PHONY: build ui ui-dev test dist clean

build: ui
	CGO_ENABLED=0 go build -ldflags "$(LDFLAGS)" -o bin/ ./cmd/...

# The dashboard's web UI (React, in ui/). The build lands in
# internal/dashboard/web/dist and is embedded into the dashboard binary.
ui: ui/node_modules
	cd ui && npm run build

ui/node_modules: ui/package.json ui/package-lock.json
	cd ui && npm ci
	touch ui/node_modules

# Hot-reloading UI on http://localhost:5173, using the API of a dashboard
# running on :9400 (GREPALERT_DASHBOARD=http://host:port to change).
ui-dev: ui/node_modules
	cd ui && npm run dev

test:
	go vet ./...
	go test ./...

# Static binaries for common targets.
dist: ui
	for t in linux/amd64 linux/arm64 windows/amd64 darwin/arm64; do \
		os=$${t%/*}; arch=$${t#*/}; ext=; [ $$os = windows ] && ext=.exe; \
		for c in grepalert-agent grepalert-dashboard; do \
			CGO_ENABLED=0 GOOS=$$os GOARCH=$$arch go build -ldflags "$(LDFLAGS)" -o dist/$$c-$$os-$$arch$$ext ./cmd/$$c; \
		done; \
	done

clean:
	rm -rf bin dist internal/dashboard/web/dist
