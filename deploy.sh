#!/bin/bash

PRIVKEY=$1

# Create fresh artifacts folder.
rm -rf artifacts
mkdir artifacts

# Return first error encountered by any command.
set -e

# Generate public key from private key.
echo "$PRIVKEY" | openssl rsa -in - -outform PEM -pubout -out artifacts/pubkey.pem

# Build binaries and sign them.
for arch in amd64 arm; do
	for os in darwin linux windows; do
	        for pkg in siac siad; do
			# Ignore unsupported arch/os combinations.
			if [ "$arch" == "arm" ]; then
				if [ "$os" == "windows" ] || [ "$os" == "darwin" ]; then
					continue
				fi
			fi

			# Binaries are called 'siac' and i'siad'.
	                bin=$pkg

			# Different naming convention for windows.
	                if [ "$os" == "windows" ]; then
	                        bin=${pkg}.exe
	                fi

			# Build binary.
	                GOOS=${os} GOARCH=${arch} go build -tags='netgo' -o artifacts/$arch/$os/$bin ./cmd/$pkg

			# Sign binary.
			echo "$PRIVKEY" | openssl dgst -sha256 -sign - -out artifacts/$arch/$os/$bin.sha256 artifacts/$arch/$os/$bin
	        done
	done
done
