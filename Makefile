.PHONY: qa backend-qa app-project app-build release release-polished notarize release-layout-qa release-qa v02-readiness v02-finalize release-open clean

VERSION ?= 0.2.0
DERIVED_DATA ?= /tmp/observatory-dd
APP_CONFIGURATION ?= Debug
DMG_STYLE ?= headless
NOTARY_PROFILE ?=
APP_NAME := Agent Observatory
APP_PROJECT := app/Observatory.xcodeproj
APP_BUNDLE = $(DERIVED_DATA)/Build/Products/$(APP_CONFIGURATION)/$(APP_NAME).app
RELEASE_APP := dist/$(APP_NAME).app
RELEASE_DMG := Agent-Observatory-$(VERSION)-macOS.dmg
RELEASE_ZIP := Agent-Observatory-$(VERSION)-macOS.zip

qa: backend-qa app-build

backend-qa:
	$(MAKE) -C backend qa

app-project:
	cd app && xcodegen generate

app-build: app-project
	rm -rf "$(APP_BUNDLE)"
	xcodebuild -project "$(APP_PROJECT)" -scheme Observatory -configuration "$(APP_CONFIGURATION)" -derivedDataPath "$(DERIVED_DATA)" build

release: APP_CONFIGURATION = Release
release: qa
	rm -rf dist
	mkdir -p dist
	cp -R "$(APP_BUNDLE)" "$(RELEASE_APP)"
	test -x "$(RELEASE_APP)/Contents/Resources/agents"
	cp "$(RELEASE_APP)/Contents/Resources/agents" dist/agents
	DMG_STYLE="$(DMG_STYLE)" scripts/make-dmg.sh "$(RELEASE_APP)" "dist/$(RELEASE_DMG)" "$(APP_NAME)"
	cd dist && ditto -c -k --keepParent "$(APP_NAME).app" "$(RELEASE_ZIP)"
	cd dist && shasum -a 256 "$(RELEASE_DMG)" "$(RELEASE_ZIP)" agents > SHA256SUMS
	@echo "release artifacts written to dist/"
	@echo "  dist/$(RELEASE_DMG)"
	@echo "  dist/$(RELEASE_ZIP)"
	@echo "  dist/agents"

release-polished: DMG_STYLE = polished
release-polished: release

notarize:
	@if [ -z "$(NOTARY_PROFILE)" ] && { [ -z "$$APP_STORE_CONNECT_KEY_ID" ] || [ -z "$$APP_STORE_CONNECT_API_KEY_P8" ]; }; then \
		echo "set NOTARY_PROFILE=<notarytool keychain profile> or APP_STORE_CONNECT_KEY_ID + APP_STORE_CONNECT_API_KEY_P8"; \
		exit 2; \
	fi
	NOTARY_PROFILE="$(NOTARY_PROFILE)" DMG_STYLE="$(DMG_STYLE)" scripts/notarize-release.sh

release-layout-qa:
	bash scripts/release-qa.sh

release-qa:
	bash scripts/release-qa.sh --notarized

v02-readiness:
	bash scripts/v02-readiness-check.sh

v02-finalize:
	bash scripts/v02-finalize.sh

release-open: release
	open "$(RELEASE_APP)"

clean:
	rm -rf dist "$(DERIVED_DATA)"
