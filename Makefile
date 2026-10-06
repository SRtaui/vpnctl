VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
PREFIX  ?= /usr/local
LDFLAGS := -s -w -X main.version=$(VERSION)

.PHONY: build test install uninstall clean

build:
	CGO_ENABLED=0 go build -trimpath -ldflags "$(LDFLAGS)" -o vpnctl .

test:
	go vet ./...
	go test ./...

install: build
	install -Dm755 vpnctl $(DESTDIR)$(PREFIX)/bin/vpnctl

uninstall:
	rm -f $(DESTDIR)$(PREFIX)/bin/vpnctl

clean:
	rm -f vpnctl
