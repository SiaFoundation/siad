#!/usr/bin/env bash
set -e

if ! [ -x "$(command -v golangci-lint)" ]; then
  echo "Installing golangci-lint..."
  go get github.com/golangci/golangci-lint/cmd/golangci-lint@v1.37.1
fi

if ! [ -x "$(command -v analyze)" ]; then
  echo "Installing analyze..."
  go get gitlab.com/NebulousLabs/analyze
fi
