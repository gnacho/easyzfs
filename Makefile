# EasyZFS — build: front (web/) → dist/ → binario estático CGO_ENABLED=0
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
BUILD   ?= $(shell date -u +%Y-%m-%dT%H:%M:%SZ)
LDFLAGS := -s -w -X main.version=$(VERSION) -X main.build=$(BUILD)

.PHONY: build web go clean

build: web go

# Front: si existe web/ se compila; si no, se usa el dist/ placeholder.
web:
	@if [ -d web ]; then \
		cd web && npm ci && npm run build && \
		cd .. && rm -rf dist && cp -r web/dist dist ; \
	else \
		echo "web/ no existe: usando dist/ placeholder" ; \
	fi

go:
	@if [ ! -d dist ]; then \
		if [ -d web/dist ]; then cp -r web/dist dist; \
		else echo "dist/ no existe: ejecuta 'make web' primero"; exit 1; fi \
	fi
	CGO_ENABLED=0 go build -trimpath -ldflags="$(LDFLAGS)" -o easyzfs .

clean:
	rm -f easyzfs
