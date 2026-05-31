#!/usr/bin/env bash
# One-command QA for the Agent Observatory backend.
# build + vet + verbose tests + race detector across all packages.
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT"

echo "==> go build ./..."
go build ./...

echo "==> go vet ./..."
go vet ./...

echo "==> go test ./... -count=1 -short"
go test ./... -count=1 -short

echo "==> go test ./... -race -count=1 -short"
go test ./... -race -count=1 -short

echo "==> install lifecycle QA (real binary, fake HOME, looped)"
bash scripts/install-qa.sh 5

echo "==> QA OK"
