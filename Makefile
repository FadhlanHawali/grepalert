VERSION ?= 0.1.0
LDFLAGS := -s -w -X github.com/fadhlanhawali/grepalert/internal/agent.Version=$(VERSION)

.PHONY: build test dist clean

build:
	CGO_ENABLED=0 go build -ldflags "$(LDFLAGS)" -o bin/ ./cmd/...

test:
	go vet ./...
	go test ./...

# Static binaries for common targets.
dist:
	for t in linux/amd64 linux/arm64 windows/amd64 darwin/arm64; do \
		os=$${t%/*}; arch=$${t#*/}; ext=; [ $$os = windows ] && ext=.exe; \
		for c in grepalert-agent grepalert-dashboard; do \
			CGO_ENABLED=0 GOOS=$$os GOARCH=$$arch go build -ldflags "$(LDFLAGS)" -o dist/$$c-$$os-$$arch$$ext ./cmd/$$c; \
		done; \
	done

clean:
	rm -rf bin dist
