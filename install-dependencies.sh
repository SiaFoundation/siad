#!/usr/bin/env bash
set -e

if ! [ -x "$(command -v golangci-lint)" ]; then
  echo "Installing golangci-lint..."
	curl -sSfL https://raw.githubusercontent.com/golangci/golangci-lint/master/install.sh | sh -s -- -b $(go env GOPATH)/bin v1.22.2
fi

if ! [ -x "$(command -v codespell)" ]; then
  echo "Installing codespell..."
  if ! [ -x "$(command -v pip3)" ]; then
    sudo apt install python3-pip
  fi
  pip3 install codespell
fi

