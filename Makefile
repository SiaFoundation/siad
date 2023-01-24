# These variables get inserted into ./build/commit.go
GIT_REVISION=$(shell git rev-parse --short HEAD)
GIT_DIRTY=$(shell git diff-index --quiet HEAD -- || echo "✗-")
ifeq ("$(GIT_DIRTY)", "✗-")
	BUILD_TIME=$(shell date)
else
	BUILD_TIME=$(shell git show -s --format=%ci HEAD)
endif

ldflags= \
-X "go.sia.tech/siad/build.BinaryName=siad" \
-X "go.sia.tech/siad/build.NodeVersion=1.5.9" \
-X "go.sia.tech/siad/build.GitRevision=${GIT_DIRTY}${GIT_REVISION}" \
-X "go.sia.tech/siad/build.BuildTime=${BUILD_TIME}"

racevars= history_size=3 halt_on_error=1 atexit_sleep_ms=2000

# all will build and install release binaries
all: release

# count says how many times to run the tests.
count = 1

# cpkg determines which package is the target when running 'make fullcover'.
# 'make fullcover' can only provide full coverage statistics on a single package
# at a time, unfortunately.
cpkg = ./modules/renter

# pkgs changes which packages the makefile calls operate on. run changes which
# tests are run during testing.
pkgs = \
	./benchmark \
	./build \
	./cmd/sia-node-scanner \
	./cmd/siac \
	./cmd/siad \
	./compatibility \
	./crypto \
	./modules \
	./modules/accounting \
	./modules/consensus \
	./modules/explorer \
	./modules/gateway \
	./modules/host \
	./modules/host/contractmanager \
	./modules/host/mdm \
	./modules/host/registry \
	./modules/miner \
	./modules/renter \
	./modules/renter/contractor \
	./modules/renter/filesystem \
	./modules/renter/filesystem/siadir \
	./modules/renter/filesystem/siafile \
	./modules/renter/hostdb \
	./modules/renter/hostdb/hosttree \
	./modules/renter/proto \
	./modules/transactionpool \
	./modules/wallet \
	./node \
	./node/api \
	./node/api/server \
	./node/api/client \
	./persist \
	./profile \
	./siatest \
	./siatest/accounting \
	./siatest/consensus \
	./siatest/daemon \
	./siatest/dependencies \
	./siatest/gateway \
	./siatest/host \
	./siatest/miner \
	./siatest/renter \
	./siatest/renter/contractor \
	./siatest/renter/hostdb \
	./siatest/renterhost \
	./siatest/transactionpool \
	./siatest/wallet \
	./sync \
	./types \
	./types/typesutil \

# release-pkgs determine which packages are built for release and distribution
# when running a 'make release' command.
release-pkgs = ./cmd/siac ./cmd/siad

# lockcheckpkgs are the packages that are checked for locking violations.
lockcheckpkgs = \
	./benchmark \
	./build \
	./cmd/sia-node-scanner \
	./cmd/siac \
	./cmd/siad \
	./node \
	./node/api \
	./node/api/client \
	./node/api/server \
	./modules/accounting \
	./modules/host/mdm \
	./modules/host/registry \
	./modules/renter/hostdb \
	./modules/renter/proto \
	./types \
	./types/typesutil \

# run determines which tests run when running any variation of 'make test'.
run = .

# util-pkgs determine the set of packages that are built when running
# 'make utils'
util-pkgs = ./cmd/sia-node-scanner

# dependencies list all packages needed to run make commands used to build, test
# and lint siac/siad locally and in CI systems.
dependencies:
	go get -d ./...
	./install-dependencies.sh

# fmt calls go fmt on all packages.
fmt:
	gofmt -s -l -w $(pkgs)

# vet calls go vet on all packages.
# NOTE: go vet requires packages to be built in order to obtain type info.
vet:
	go vet $(pkgs)

analyze:
	analyze -lockcheck=false -- $(pkgs)
	analyze -lockcheck -- $(lockcheckpkgs)

# lint runs golangci-lint.
lint:
	golangci-lint run -c .golangci.yml ./...
	analyze -lockcheck=false -- $(pkgs)
	analyze -lockcheck -- $(lockcheckpkgs)

# staticcheck runs the staticcheck tool
# NOTE: this is not yet enabled in the CI system.
staticcheck:
	staticcheck $(pkgs)

# debug builds and installs debug binaries. This will also install the utils.
debug:
	go install -tags='debug profile netgo' -ldflags='$(ldflags)' $(pkgs)
debug-race:
	GORACE='$(racevars)' go install -race -tags='debug profile netgo' -ldflags='$(ldflags)' $(pkgs)

# dev builds and installs developer binaries. This will also install the utils.
dev:
	go install -tags='dev debug profile netgo' -ldflags='$(ldflags)' $(pkgs)
dev-race:
	GORACE='$(racevars)' go install -race -tags='dev debug profile netgo' -ldflags='$(ldflags)' $(pkgs)

static:
	go build -trimpath -o release/ -tags='netgo' -ldflags='-s -w $(ldflags)' $(release-pkgs)

testnet:
	CGO_ENABLED=0 go build -trimpath -o release/ -tags='netgo testnet' -ldflags='-s -w $(ldflags)' $(release-pkgs)

# release builds and installs release binaries.
release:
	go install -tags='netgo' -ldflags='-s -w $(ldflags)' $(release-pkgs)
release-race:
	GORACE='$(racevars)' go install -race -tags='netgo' -ldflags='-s -w $(ldflags)' $(release-pkgs)
release-util:
	go install -tags='netgo' -ldflags='-s -w $(ldflags)' $(release-pkgs) $(util-pkgs)

# clean removes all directories that get automatically created during
# development.
clean:
ifneq ("$(OS)","Windows_NT")
# Linux
	rm -rf cover doc/whitepaper.aux doc/whitepaper.log doc/whitepaper.pdf fullcover release
else
# Windows
	- DEL /F /Q cover doc\whitepaper.aux doc\whitepaper.log doc\whitepaper.pdf fullcover release
endif

test:
	go test -short -tags='debug testing netgo' -timeout=5s $(pkgs) -run=$(run) -count=$(count)
test-v:
	GORACE='$(racevars)' go test -race -v -short -tags='debug testing netgo' -timeout=15s $(pkgs) -run=$(run) -count=$(count)
test-long: clean fmt vet lint
	@mkdir -p cover
	GORACE='$(racevars)' go test -race --coverprofile='./cover/cover.out' -v -failfast -tags='testing debug netgo' -timeout=3600s $(pkgs) -run=$(run) -count=$(count)

# Use on Linux (and MacOS)
test-vlong: clean fmt vet lint
	@mkdir -p cover
	GORACE='$(racevars)' go test --coverprofile='./cover/cover.out' -v -race -tags='testing debug vlong netgo' -timeout=20000s $(pkgs) -run=$(run) -count=$(count)

# Use on Windows without fmt, vet, lint
test-vlong-windows: clean
	MD cover
	SET GORACE='$(racevars)'
	go test --coverprofile='./cover/cover.out' -v -race -tags='testing debug vlong netgo' -timeout=20000s $(pkgs) -run=$(run) -count=$(count)

test-cpu:
	go test -v -tags='testing debug netgo' -timeout=500s -cpuprofile cpu.prof $(pkgs) -run=$(run) -count=$(count)
test-mem:
	go test -v -tags='testing debug netgo' -timeout=500s -memprofile mem.prof $(pkgs) -run=$(run) -count=$(count)
bench: clean fmt
	go test -tags='debug testing netgo' -timeout=500s -run=XXX -bench=$(run) $(pkgs) -count=$(count)
cover: clean
	@mkdir -p cover
	@for package in $(pkgs); do                                                                                                                                 \
		mkdir -p `dirname cover/$$package`                                                                                                                      \
		&& go test -tags='testing debug netgo' -timeout=500s -covermode=atomic -coverprofile=cover/$$package.out ./$$package -run=$(run) || true 				\
		&& go tool cover -html=cover/$$package.out -o=cover/$$package.html ;                                                                                    \
	done

# fullcover is a command that will give the full coverage statistics for a
# package. Unlike the 'cover' command, full cover will include the testing
# coverage that is provided by all tests in all packages on the target package.
# Only one package can be targeted at a time. Use 'cpkg' as the variable for the
# target package, 'pkgs' as the variable for the packages running the tests.
#
# NOTE: this command has to run the full test suite to get output for a single
# package. Ideally we could get the output for all packages when running the
# full test suite.
#
# NOTE: This command will not skip testing packages that do not run code in the
# target package at all. For example, none of the tests in the 'sync' package
# will provide any coverage to the renter package. The command will not detect
# this and will run all of the sync package tests anyway.
fullcover: clean
	@mkdir -p fullcover
	@mkdir -p fullcover/tests
	@echo "mode: atomic" >> fullcover/fullcover.out
	@for package in $(pkgs); do                                                                                                                                                             \
		mkdir -p `dirname fullcover/tests/$$package`                                                                                                                                        \
		&& go test -tags='testing debug netgo' -timeout=500s -covermode=atomic -coverprofile=fullcover/tests/$$package.out -coverpkg $(cpkg) ./$$package -run=$(run) || true 				\
		&& go tool cover -html=fullcover/tests/$$package.out -o=fullcover/tests/$$package.html                                                                                              \
		&& tail -n +2 fullcover/tests/$$package.out >> fullcover/fullcover.out ;                                                                                                            \
	done
	@go tool cover -html=fullcover/fullcover.out -o fullcover/fullcover.html
	@printf 'Full coverage on $(cpkg):'
	@go tool cover -func fullcover/fullcover.out | tail -n -1 | awk '{$$1=""; $$2=""; sub(" ", " "); print}'

# whitepaper builds the whitepaper from whitepaper.tex. pdflatex has to be
# called twice because references will not update correctly the first time.
whitepaper:
	@pdflatex -output-directory=doc whitepaper.tex > /dev/null
	pdflatex -output-directory=doc whitepaper.tex

.PHONY: all fmt install release clean test test-v test-long cover whitepaper

