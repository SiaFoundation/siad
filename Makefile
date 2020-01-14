# These variables get inserted into ./build/commit.go
BUILD_TIME=$(shell date)
GIT_REVISION=$(shell git rev-parse --short HEAD)
GIT_DIRTY=$(shell git diff-index --quiet HEAD -- || echo "✗-")

ldflags= -X gitlab.com/NebulousLabs/Sia/build.GitRevision=${GIT_DIRTY}${GIT_REVISION} \
-X "gitlab.com/NebulousLabs/Sia/build.BuildTime=${BUILD_TIME}"

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
pkgs = ./build \
	./cmd/sia-node-scanner \
	./cmd/siac \
	./cmd/siad \
	./compatibility \
	./crypto \
	./encoding \
	./modules \
	./modules/consensus \
	./modules/explorer \
	./modules/gateway \
	./modules/host \
	./modules/host/contractmanager \
	./modules/host/mdm \
	./modules/miner \
	./modules/renter \
	./modules/renter/contractor \
	./modules/renter/filesystem \
	./modules/renter/hostdb \
	./modules/renter/hostdb/hosttree \
	./modules/renter/proto \
	./modules/renter/siadir \
	./modules/renter/siafile \
	./modules/transactionpool \
	./modules/wallet \
	./node \
	./node/api \
	./node/api/server \
	./node/api/client \
	./persist \
	./profile \
	./siatest \
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
	./types/typesutil

# release-pkgs determine which packages are built for release and distrubtion
# when running a 'make release' command.
release-pkgs = ./cmd/siac ./cmd/siad

# run determines which tests run when running any variation of 'make test'.
run = .

# util-pkgs determine the set of packages that are built when running
# 'make utils'
util-pkgs = ./cmd/sia-node-scanner

# fmt calls go fmt on all packages.
fmt:
	gofmt -s -l -w $(pkgs)

# vet calls go vet on all packages.
# NOTE: go vet requires packages to be built in order to obtain type info.
vet:
	GO111MODULE=on go vet $(pkgs)

lint:
	GO111MODULE=on go get golang.org/x/lint/golint
	golint -min_confidence=1.0 -set_exit_status $(pkgs)
	GO111MODULE=on go run ./analysis/cmd/analyze.go -- $(pkgs)

lint-analysis:
	GO111MODULE=on go run ./analysis/cmd/analyze.go -- $(pkgs)

lint-all:
	GO111MODULE=on go run ./analysis/cmd/analyze.go -- $(pkgs)
	golangci-lint run -c .golangci.yml

# markdown-spellcheck runs codespell on all markdown files that are not
# vendored.
markdown-spellcheck:
	git ls-files "*.md" :\!:"vendor/**" | xargs codespell --check-filenames

# spellcheck checks for misspelled words in comments or strings.
spellcheck:
	misspell -error .

# staticcheck runs the staticcheck tool
staticcheck:
	staticcheck $(pkgs)

# debug builds and installs debug binaries. This will also install the utils.
debug:
	GO111MODULE=on go install -tags='debug profile netgo' -ldflags='$(ldflags)' $(pkgs)
debug-race:
	GO111MODULE=on go install -race -tags='debug profile netgo' -ldflags='$(ldflags)' $(pkgs)

# dev builds and installs developer binaries. This will also install the utils.
dev:
	GO111MODULE=on go install -tags='dev debug profile netgo' -ldflags='$(ldflags)' $(pkgs)
dev-race:
	GO111MODULE=on go install -race -tags='dev debug profile netgo' -ldflags='$(ldflags)' $(pkgs)

# release builds and installs release binaries.
release:
	GO111MODULE=on go install -tags='netgo' -ldflags='-s -w $(ldflags)' $(release-pkgs)
release-race:
	GO111MODULE=on go install -race -tags='netgo' -ldflags='-s -w $(ldflags)' $(release-pkgs)

# clean removes all directories that get automatically created during
# development.
clean:
	rm -rf cover doc/whitepaper.aux doc/whitepaper.log doc/whitepaper.pdf fullcover release 

test:
	GO111MODULE=on go test -short -tags='debug testing netgo' -timeout=5s $(pkgs) -run=$(run) -count=$(count)
test-v:
	GO111MODULE=on go test -race -v -short -tags='debug testing netgo' -timeout=15s $(pkgs) -run=$(run) -count=$(count)
test-long: clean fmt vet lint
	@mkdir -p cover
	GO111MODULE=on go test --coverprofile='./cover/cover.out' -v -race -failfast -tags='testing debug netgo' -timeout=1800s $(pkgs) -run=$(run) -count=$(count)
test-vlong: clean fmt vet lint
	@mkdir -p cover
	GO111MODULE=on go test --coverprofile='./cover/cover.out' -v -race -tags='testing debug vlong netgo' -timeout=20000s $(pkgs) -run=$(run) -count=$(count)
test-cpu:
	GO111MODULE=on go test -v -tags='testing debug netgo' -timeout=500s -cpuprofile cpu.prof $(pkgs) -run=$(run) -count=$(count)
test-mem:
	GO111MODULE=on go test -v -tags='testing debug netgo' -timeout=500s -memprofile mem.prof $(pkgs) -run=$(run) -count=$(count)
bench: clean fmt
	GO111MODULE=on go test -tags='debug testing netgo' -timeout=500s -run=XXX -bench=$(run) $(pkgs) -count=$(count)
cover: clean
	@mkdir -p cover
	@for package in $(pkgs); do                                                                                                                                 \
		mkdir -p `dirname cover/$$package`                                                                                                                      \
		&& GO111MODULE=on go test -tags='testing debug netgo' -timeout=500s -covermode=atomic -coverprofile=cover/$$package.out ./$$package -run=$(run) || true \
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
		&& GO111MODULE=on go test -tags='testing debug netgo' -timeout=500s -covermode=atomic -coverprofile=fullcover/tests/$$package.out -coverpkg $(cpkg) ./$$package -run=$(run) || true \
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

