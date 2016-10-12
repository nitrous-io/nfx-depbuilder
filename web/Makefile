release: build
	docker build -t nfx-depbuilder-web .
.PHONY: release

build:
	docker run --rm -e CGO_ENABLED=0 -e GOOS=linux \
		-v ${CURDIR}:/go/src/github.com/nitrous-io/nfx-depbuilder/web \
		-w /go/src/github.com/nitrous-io/nfx-depbuilder/web golang:1.7.1-alpine \
		go build -a --ldflags '-extldflags "-static"' \
		-o /go/src/github.com/nitrous-io/nfx-depbuilder/web/build/webserver \
		github.com/nitrous-io/nfx-depbuilder/web/webserver
.PHONY: build
