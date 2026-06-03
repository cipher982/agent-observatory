import AppKit
import SwiftUI

@MainActor
final class AppDelegate: NSObject, NSApplicationDelegate, NSMenuDelegate {
    private var statusItem: NSStatusItem?
    private var engine: EngineClient?
    private var proxy: ProxyController?

    func applicationDidFinishLaunching(_ notification: Notification) {
        installStatusItemIfNeeded()
    }

    func configure(engine: EngineClient, proxy: ProxyController) {
        self.engine = engine
        self.proxy = proxy
        installStatusItemIfNeeded()
    }

    private func installStatusItemIfNeeded() {
        guard statusItem == nil else { return }
        let item = NSStatusBar.system.statusItem(withLength: NSStatusItem.squareLength)
        item.isVisible = true
        item.behavior = [.removalAllowed, .terminationOnRemoval]
        item.autosaveName = "AgentObservatoryStatusItem"

        if let button = item.button {
            let image = NSImage(
                systemSymbolName: "antenna.radiowaves.left.and.right",
                accessibilityDescription: "Agent Observatory"
            ) ?? NSImage(systemSymbolName: "scope", accessibilityDescription: "Agent Observatory")
            image?.isTemplate = true
            button.image = image
            button.toolTip = "Agent Observatory"
        }

        let menu = NSMenu()
        menu.delegate = self
        item.menu = menu
        statusItem = item
    }

    func menuNeedsUpdate(_ menu: NSMenu) {
        menu.removeAllItems()

        menu.addItem(NSMenuItem(
            title: "Show Agent Observatory",
            action: #selector(showApp),
            keyEquivalent: ""
        ))
        menu.addItem(.separator())

        let mode = engine?.mode == .demo ? "Demo ready" : "Live capture"
        let connection = engine?.streamConnected == true ? "Connected" : "Reconnecting"
        menu.addDisabledItem(title: mode)
        menu.addDisabledItem(title: connection)

        let captureOn = proxy?.isActive == true
        menu.addDisabledItem(title: captureOn ? "Capture extension: on" : "Capture extension: off")
        menu.addItem(.separator())

        let captureTitle = captureOn ? "Disable Live Capture" : "Enable Live Capture"
        let captureItem = NSMenuItem(title: captureTitle, action: #selector(toggleCapture), keyEquivalent: "")
        captureItem.isEnabled = proxy?.status != .activating
        menu.addItem(captureItem)

        let resetItem = NSMenuItem(title: "Reset Capture Config", action: #selector(resetCaptureConfig), keyEquivalent: "")
        resetItem.isEnabled = proxy?.status != .activating
        menu.addItem(resetItem)

        let modeTitle = engine?.mode == .demo ? "Switch to Live Mode" : "Switch to Demo Mode"
        menu.addItem(NSMenuItem(title: modeTitle, action: #selector(toggleMode), keyEquivalent: ""))

        menu.addItem(NSMenuItem(title: "Show Onboarding", action: #selector(showOnboarding), keyEquivalent: ""))
        menu.addItem(NSMenuItem(title: "Refresh Sessions", action: #selector(refreshSessions), keyEquivalent: ""))

        menu.addItem(.separator())
        menu.addItem(NSMenuItem(title: "Quit Agent Observatory", action: #selector(quitApp), keyEquivalent: "q"))
    }

    @objc private func showApp() {
        NSWorkspace.shared.openApplication(at: Bundle.main.bundleURL, configuration: NSWorkspace.OpenConfiguration())
        NSApplication.shared.activate(ignoringOtherApps: true)
        for window in NSApplication.shared.windows {
            window.makeKeyAndOrderFront(nil)
        }
    }

    @objc private func toggleCapture() {
        guard let proxy else { return }
        if proxy.isActive {
            proxy.deactivate()
        } else {
            proxy.activate()
        }
    }

    @objc private func resetCaptureConfig() {
        proxy?.resetCaptureConfiguration()
    }

    @objc private func toggleMode() {
        guard let engine else { return }
        engine.restart(mode: engine.mode == .demo ? .live : .demo)
    }

    @objc private func showOnboarding() {
        UserDefaults.standard.set(false, forKey: "observatory.onboarding.completed")
        UserDefaults.standard.set(true, forKey: "observatory.onboarding.visible")
        if engine?.mode != .demo {
            engine?.restart(mode: .demo)
        }
        showApp()
    }

    @objc private func refreshSessions() {
        guard let engine else { return }
        Task { await engine.refresh() }
    }

    @objc private func quitApp() {
        NSApplication.shared.terminate(nil)
    }
}

private extension NSMenu {
    func addDisabledItem(title: String) {
        let item = NSMenuItem(title: title, action: nil, keyEquivalent: "")
        item.isEnabled = false
        addItem(item)
    }
}
