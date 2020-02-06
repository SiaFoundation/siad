#!/usr/bin/env bash
set -e

echo "$0 builds Sia in a reproducible Docker build environment"

branchName="$1"
versionName="$2"

if [ -z $branchName ] || [ -z $versionName ]; then
  echo "Usage: $0 BRANCHNAME VERSION"
  exit 1
fi

echo Branch name: ${branchName}
echo Version: ${versionName}
echo ""

# Sleep for a bit to give user chance to quit.
sleep 2

# Build the image uncached to always get the correct
docker build -t sia-builder . --build-arg branch=${branchName} --build-arg version=${versionName}

# Create a container with the artifacts.
docker create --name build-container sia-builder

# Copy the artifacts out.
docker cp build-container:/home/builder/Sia/release/ ../

# Remove the build container.
docker rm build-container

# Package the binaries produced.
./package.sh ${versionName} ../release/

# Print out the SHA256SUM file.
echo "SHA256SUM of binaries built: "
cat ../release/Sia-${versionName}-SHA256SUMS.txt
