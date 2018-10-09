#!/bin/bash

PRIVKEY=$1

# Create fresh artifacts folder.
rm -rf artifacts
mkdir artifacts

# Generate public key from private key.
echo "$PRIVKEY" | openssl rsa -in - -outform PEM -pubout -out artifacts/pubkey.pem
if [ $? -ne 0 ]; then
	exit $?
fi

# Build binaries and sign them.
for arch in amd64 arm; do
	for os in darwin linux windows; do
	        for pkg in siac siad; do
			if [ "$arch" == "arm" ]; then
				if [ "$os" == "windows" ] || [ "$os" == "darwin" ]; then
					continue
				fi
			fi

	                bin=$pkg
	                if [ "$os" == "windows" ]; then
	                        bin=${pkg}.exe
	                fi

	                GOOS=${os} GOARCH=${arch} go build -tags='netgo' -o artifacts/$arch/$os/$bin ./cmd/$pkg
			if [ $? -ne 0 ]; then
    				exit $?
			fi

			echo "$PRIVKEY" | openssl dgst -sha256 -sign - -out artifacts/$arch/$os/$bin.sha256 artifacts/$arch/$os/$bin
			if [ $? -ne 0 ]; then
    				exit $?
			fi
	        done
	done
done
