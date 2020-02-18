#!/usr/bin/env bash
set -e

version="$1"

# Directory where the binaries were placed. 
binDir="$2"

function package {
  os=$1
  arch=$2
 	
	echo Packaging ${os}...
 	folder=$binDir/Sia-$version-$os-$arch
 	(
		cd $binDir
		zip -rq Sia-$version-$os-$arch.zip Sia-$version-$os-$arch
		sha256sum  Sia-$version-$os-$arch.zip >> Sia-$version-SHA256SUMS.txt
 	)
}

# Package amd64 binaries.
for os in darwin linux windows; do
  package "$os" "amd64"
done

# Package Raspberry Pi binaries.
package "linux" "arm64"
