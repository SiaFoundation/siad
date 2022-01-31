-include .env.local

ifeq "$(shell git status --porcelain=v1 2>/dev/null)" "" 
GIT_REVISION=$(shell git rev-parse --short HEAD)
BUILD_TIME=$(shell git show -s --format=%ci HEAD)
else
GIT_REVISION="$(shell git rev-parse --short HEAD)-dev"
BUILD_TIME=$(shell date)
endif

# get the current OS and arch
OS=$(shell GOOS= go env GOOS)
ARCH=$(shell GOARCH= go env GOARCH)

# get the target OS and arch
GOOS?=$(shell go env GOOS)
GOARCH?=$(shell go env GOARCH)

lint:
	golangci-lint run

test:
	go test -count=1 -race ./...

build-siad:
	CGO_ENABLED=0 go build -tags='netgo' -trimpath -ldflags "-X 'go.sia.tech/siad/cmd/renterd/githash=${GIT_REVISION}' -X 'go.sia.tech/siad/cmd/renterd/builddate=${BUILD_TIME}' -s -w" -o ./bin/ ./cmd/siad ./cmd/siac

docker-siad:
	docker build -t ghcr.io/siafoundation/siad:v2 -f siad.Dockerfile .

build-renter:
# TODO: build web assets
	CGO_ENABLED=0 go build -tags='netgo' -trimpath -ldflags "-X 'go.sia.tech/siad/cmd/renterd/githash=${GIT_REVISION}' -X 'go.sia.tech/siad/cmd/renterd/builddate=${BUILD_TIME}' -s -w" -o ./bin/ ./cmd/renterd

docker-renter:
	docker build -t ghcr.io/siafoundation/renterd -f renterd.Dockerfile .