.PHONY: qa build test race doctor

qa:
	@bash scripts/qa.sh

build:
	go build ./...

test:
	go test ./... -count=1 -short

race:
	go test ./... -race -count=1 -short

# Build the binary into the SwiftUI app's resources dir for bundling.
embed:
	go build -o ../app/agents ./cmd/agents
