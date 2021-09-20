#!/usr/bin/env bash
set -e

# version and keys are supplied as arguments
version="$1"
rc=`echo $version | awk -F - '{print $2}'`
if [[ -z $version ]]; then
	echo "Usage: $0 VERSION"
	exit 1
fi

function build {
  os=$1
  arch=$2

	echo Building ${os}...
	# create workspace
	folder=release/Sia-$version-$os-$arch
	rm -rf $folder
	mkdir -p $folder

	GOOS=$1 GOARCH=$2 make static

	# compile and hash binaries
	for pkg in siac siad; do
		bin=$pkg
		if [ "$os" == "windows" ]; then
			bin=${pkg}.exe
		fi

		mv release/$bin $folder

		(
			cd release/
			sha256sum Sia-$version-$os-$arch/$bin >> Sia-$version-SHA256SUMS.txt
		)
  	done

	cp -r doc LICENSE README.md $folder
}

# Build amd64 binaries.
for os in darwin linux windows; do
  build "$os" "amd64"
done

# Build Raspberry Pi binaries.
build "linux" "arm64"
