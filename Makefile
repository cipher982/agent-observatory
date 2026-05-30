.PHONY: qa backend-qa app-project app-build release clean

VERSION ?= 0.1.0
DERIVED_DATA ?= /tmp/observatory-dd
APP_PROJECT := app/Observatory.xcodeproj
APP_BUNDLE := $(DERIVED_DATA)/Build/Products/Debug/Observatory.app

qa: backend-qa app-build

backend-qa:
	$(MAKE) -C backend qa

app-project:
	cd app && xcodegen generate

app-build: app-project
	xcodebuild -project $(APP_PROJECT) -scheme Observatory -configuration Debug -derivedDataPath $(DERIVED_DATA) build

release: qa
	rm -rf dist
	mkdir -p dist
	cp -R "$(APP_BUNDLE)" dist/Observatory.app
	cp "$(APP_BUNDLE)/Contents/Resources/agents" dist/agents
	cd dist && ditto -c -k --keepParent Observatory.app "Agent-Context-Observatory-$(VERSION)-macos.zip"
	cd dist && shasum -a 256 "Agent-Context-Observatory-$(VERSION)-macos.zip" agents > SHA256SUMS
	@echo "release artifacts written to dist/"

clean:
	rm -rf dist "$(DERIVED_DATA)"
