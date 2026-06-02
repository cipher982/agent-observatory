import AppKit
import SwiftUI

// First-run onboarding. A calm, linear flow: one headline, a demo escape hatch,
// and a short stepper whose single primary action is whatever the user needs to
// do next (install → enable → go live). Dense trust/CLI detail lives behind
// disclosures so the default screen stays quiet. All step state is DERIVED from
// engine.installReady + proxy.status; there is no separate onboarding state.
struct OnboardingView: View {
    @Environment(EngineClient.self) private var engine
    @Environment(ProxyController.self) private var proxy

    let onExploreDemo: () -> Void
    let onUseLive: () -> Void

    @State private var commandCopied = false
    @State private var uninstallCopied = false
    @State private var showTrust = false
    @State private var showAdvanced = false

    var body: some View {
        ScrollView {
            VStack(alignment: .leading, spacing: 22) {
                header
                if proxy.isActive {
                    liveOnCard
                } else {
                    stepper
                }
                disclosures
            }
            .padding(.horizontal, 32)
            .padding(.vertical, 28)
            .frame(maxWidth: 760)
            .frame(maxWidth: .infinity)
        }
        .scrollEdgeEffectStyle(.soft, for: .top)
        .task {
            await engine.checkInstallStatus()
            proxy.refreshStatus()
        }
    }

    // MARK: header

    private var header: some View {
        VStack(alignment: .leading, spacing: 14) {
            Label("Local-first · sample data already running", systemImage: "lock.shield")
                .font(.caption.weight(.semibold))
                .foregroundStyle(.mint)

            Text("See what your agents actually send.")
                .font(.system(size: 40, weight: .bold, design: .rounded))
                .foregroundStyle(.white)
                .fixedSize(horizontal: false, vertical: true)

            Text("Observatory starts with a live demo feed. Turn on live capture when you want to verify your own agents — it routes only LLM-provider traffic through a local proxy and stores derived facts, never raw prompts.")
                .font(.title3)
                .foregroundStyle(.secondary)
                .fixedSize(horizontal: false, vertical: true)

            Button { onExploreDemo() } label: {
                Label("Explore the demo feed", systemImage: "play.circle.fill")
            }
            .buttonStyle(.bordered)
            .controlSize(.large)
        }
    }

    // MARK: stepper (shown until capture is active)

    private var stepper: some View {
        VStack(alignment: .leading, spacing: 18) {
            Text("Turn on live capture")
                .font(.title2.weight(.bold))

            if let warning = engine.helperLocationWarning {
                Label(warning, systemImage: "exclamationmark.triangle.fill")
                    .font(.caption.weight(.semibold))
                    .foregroundStyle(.orange)
                    .fixedSize(horizontal: false, vertical: true)
            }

            StepRow(index: 1, state: installStepState,
                    title: "Install the local engine",
                    detail: installStepDetail)
            if !engine.installReady {
                installCommandRow
            }

            StepRow(index: 2, state: enableStepState,
                    title: "Enable the capture extension",
                    detail: enableStepDetail)

            // The one primary action: enable (or retry). Disabled until the
            // engine is installed and while an approval is mid-flight.
            HStack(spacing: 10) {
                Button { proxy.activate() } label: {
                    Label(enableButtonTitle, systemImage: "bolt.circle.fill")
                        .frame(maxWidth: .infinity)
                }
                .buttonStyle(.borderedProminent)
                .controlSize(.large)
                .disabled(!engine.installReady || proxy.status == .activating)

                Button {
                    Task { await engine.checkInstallStatus() }
                    proxy.refreshStatus()
                } label: {
                    Label("Refresh", systemImage: "arrow.clockwise")
                }
                .buttonStyle(.bordered)
                .controlSize(.large)
            }

            if let line = statusLine {
                Text(line.text)
                    .font(.callout.weight(.medium))
                    .foregroundStyle(line.color)
                    .fixedSize(horizontal: false, vertical: true)
            }
        }
        .padding(22)
        .glassEffect(.regular.tint(Color.black.opacity(0.22)),
                     in: RoundedRectangle(cornerRadius: 16, style: .continuous))
    }

    private var installCommandRow: some View {
        HStack(spacing: 10) {
            Text(engine.installCommandPreview)
                .font(.caption.monospaced())
                .lineLimit(2)
                .truncationMode(.middle)
                .padding(.horizontal, 12)
                .padding(.vertical, 10)
                .frame(maxWidth: .infinity, alignment: .leading)
                .glassEffect(.regular.tint(Color.black.opacity(0.24)),
                             in: RoundedRectangle(cornerRadius: 8, style: .continuous))
                .textSelection(.enabled)
            Button { copyInstallCommand() } label: {
                Label(commandCopied ? "Copied" : "Copy", systemImage: commandCopied ? "checkmark" : "doc.on.doc")
            }
            .buttonStyle(.bordered)
            .disabled(!engine.installCommandAvailable)
        }
    }

    // MARK: live-on card (shown when capture is active)

    private var liveOnCard: some View {
        VStack(alignment: .leading, spacing: 14) {
            Label("Live capture is on", systemImage: "checkmark.seal.fill")
                .font(.title2.weight(.bold))
                .foregroundStyle(.green)
            Text("The system extension is approved and routing provider flows to Observatory. Your other traffic is untouched.")
                .font(.callout)
                .foregroundStyle(.secondary)
                .fixedSize(horizontal: false, vertical: true)
            HStack(spacing: 10) {
                Button { onUseLive() } label: {
                    Label("Go to live feed", systemImage: "arrow.right.circle.fill")
                }
                .buttonStyle(.borderedProminent)
                .controlSize(.large)
                Button(role: .destructive) { proxy.deactivate() } label: {
                    Label("Disable capture", systemImage: "stop.circle")
                }
                .buttonStyle(.bordered)
                .controlSize(.large)
            }
        }
        .padding(22)
        .glassEffect(.regular.tint(Color.green.opacity(0.16)),
                     in: RoundedRectangle(cornerRadius: 16, style: .continuous))
    }

    // MARK: disclosures (calm by default)

    private var disclosures: some View {
        VStack(alignment: .leading, spacing: 8) {
            DisclosureGroup(isExpanded: $showTrust) {
                TrustBoundaryPanel().padding(.top, 8)
            } label: {
                Label("Trust & privacy", systemImage: "shield.lefthalf.filled")
                    .font(.callout.weight(.semibold))
            }

            DisclosureGroup(isExpanded: $showAdvanced) {
                advancedPanel.padding(.top, 8)
            } label: {
                Label("Advanced · CLI & reset", systemImage: "terminal")
                    .font(.callout.weight(.semibold))
            }
        }
        .padding(.horizontal, 4)
    }

    private var advancedPanel: some View {
        VStack(alignment: .leading, spacing: 12) {
            Text("Install command (bundled helper):")
                .font(.caption).foregroundStyle(.secondary)
            HStack(spacing: 10) {
                Text(engine.installCommandPreview)
                    .font(.caption.monospaced())
                    .lineLimit(2).truncationMode(.middle)
                    .padding(.horizontal, 10).padding(.vertical, 8)
                    .frame(maxWidth: .infinity, alignment: .leading)
                    .background(.black.opacity(0.16), in: RoundedRectangle(cornerRadius: 8, style: .continuous))
                    .textSelection(.enabled)
                Button { copyInstallCommand() } label: {
                    Label(commandCopied ? "Copied" : "Copy", systemImage: commandCopied ? "checkmark" : "doc.on.doc")
                }
                .buttonStyle(.bordered)
                .disabled(!engine.installCommandAvailable)
            }
            Text("Reset / uninstall:")
                .font(.caption).foregroundStyle(.secondary)
            Button(role: .destructive) { proxy.resetCaptureConfiguration() } label: {
                Label("Reset Capture Config", systemImage: "arrow.triangle.2.circlepath")
            }
            .buttonStyle(.bordered)
            .disabled(proxy.status == .activating)
            HStack(spacing: 10) {
                Text(engine.uninstallCommand)
                    .font(.caption.monospaced())
                    .lineLimit(1).truncationMode(.middle)
                    .padding(.horizontal, 10).padding(.vertical, 8)
                    .frame(maxWidth: .infinity, alignment: .leading)
                    .background(.black.opacity(0.16), in: RoundedRectangle(cornerRadius: 8, style: .continuous))
                    .textSelection(.enabled)
                Button { copyUninstallCommand() } label: {
                    Label(uninstallCopied ? "Copied" : "Copy", systemImage: uninstallCopied ? "checkmark" : "trash")
                }
                .buttonStyle(.bordered)
                .disabled(!engine.installCommandAvailable)
            }
        }
    }

    // MARK: derived step state

    private var installStepState: StepRow.State { engine.installReady ? .done : .current }

    private var enableStepState: StepRow.State {
        if proxy.isActive { return .done }
        if !engine.installReady { return .pending }
        if case .failed = proxy.status { return .current }
        return .current
    }

    private var installStepDetail: String {
        engine.installReady
            ? "Installed — proxy daemon, local CA, and additive Node trust are in place."
            : "Run the bundled command below once. It installs the local proxy daemon, a local CA, and one additive Node trust var. Fully reversible."
    }

    private var enableStepDetail: String {
        switch proxy.status {
        case .active: return "Approved and routing provider flows. Trust for the local CA is installed in your login keychain."
        case .activating: return "Approve the system extension in System Settings → General → Login Items & Extensions, then allow the keychain prompt."
        case .needsApproval: return "Approve “Agent Observatory” in System Settings → General → Login Items & Extensions, then allow the keychain prompt."
        case .failed(let m): return m
        default: return "Approve the macOS system extension once, then allow the login-keychain trust prompt. Capture starts immediately after."
        }
    }

    private var enableButtonTitle: String {
        switch proxy.status {
        case .activating: return "Enabling…"
        case .needsApproval: return "Waiting for approval…"
        case .failed: return "Try again"
        default: return "Enable Live Capture"
        }
    }

    private var statusLine: (text: String, color: Color)? {
        switch proxy.status {
        case .unknown, .inactive:
            return engine.installReady ? nil : (engine.installStatusText, installStatusColor)
        case .activating:
            return ("Enabling capture — approve the system extension if macOS prompts you.", .orange)
        case .needsApproval:
            return ("Waiting for approval in System Settings → General → Login Items & Extensions.", .orange)
        case .active:
            return ("Capture is active.", .green)
        case .failed(let m):
            return ("Couldn’t enable capture: \(m)", .red)
        }
    }

    private var installStatusColor: Color {
        switch engine.installState {
        case .installed: return .green
        case .partial: return .orange
        case .unavailable: return .red
        default: return .secondary
        }
    }

    private func copyInstallCommand() {
        NSPasteboard.general.clearContents()
        NSPasteboard.general.setString(engine.installCommand, forType: .string)
        commandCopied = true
        Task { try? await Task.sleep(for: .seconds(1.2)); await MainActor.run { commandCopied = false } }
    }

    private func copyUninstallCommand() {
        NSPasteboard.general.clearContents()
        NSPasteboard.general.setString(engine.uninstallCommand, forType: .string)
        uninstallCopied = true
        Task { try? await Task.sleep(for: .seconds(1.2)); await MainActor.run { uninstallCopied = false } }
    }
}

// One row of the linear setup stepper.
private struct StepRow: View {
    enum State { case done, current, pending }
    let index: Int
    let state: State
    let title: String
    let detail: String

    var body: some View {
        HStack(alignment: .top, spacing: 12) {
            badge
            VStack(alignment: .leading, spacing: 2) {
                Text(title)
                    .font(.callout.weight(.semibold))
                    .foregroundStyle(state == .pending ? .secondary : .primary)
                Text(detail)
                    .font(.caption)
                    .foregroundStyle(.secondary)
                    .fixedSize(horizontal: false, vertical: true)
            }
            Spacer(minLength: 0)
        }
    }

    @ViewBuilder private var badge: some View {
        switch state {
        case .done:
            Image(systemName: "checkmark.circle.fill").foregroundStyle(.green)
        case .current:
            Image(systemName: "\(index).circle.fill").foregroundStyle(.blue)
        case .pending:
            Image(systemName: "\(index).circle").foregroundStyle(.secondary)
        }
    }
}

private struct TrustBoundaryPanel: View {
    var body: some View {
        VStack(alignment: .leading, spacing: 12) {
            Text("A system extension routes only known LLM provider flows to Observatory's loopback proxy; everything else is untouched. Agents trust the proxy via a local CA in your login keychain. The proxy extracts derived request facts, not stored raw prompt bodies.")
                .font(.callout)
                .foregroundStyle(.primary)
                .fixedSize(horizontal: false, vertical: true)

            Grid(alignment: .leading, horizontalSpacing: 14, verticalSpacing: 10) {
                GridRow {
                    TrustFact(symbol: "checkmark.seal.fill", title: "Inspected", detail: "OpenAI, Anthropic, and Bedrock request bodies")
                    TrustFact(symbol: "lock.fill", title: "Opaque", detail: "Unrelated HTTPS hosts tunnel through unread")
                }
                GridRow {
                    TrustFact(symbol: "internaldrive", title: "Stored", detail: "Endpoint, prompt length, tool names, evidence marks")
                    TrustFact(symbol: "trash", title: "Reversible", detail: "Reset disables capture config; uninstall removes daemon, keychain trust, and CA state")
                }
            }
        }
        .padding(14)
        .background(.black.opacity(0.18), in: RoundedRectangle(cornerRadius: 10, style: .continuous))
    }
}

private struct TrustFact: View {
    let symbol: String
    let title: String
    let detail: String

    var body: some View {
        HStack(alignment: .top, spacing: 8) {
            Image(systemName: symbol)
                .foregroundStyle(.mint)
                .frame(width: 20)
            VStack(alignment: .leading, spacing: 2) {
                Text(title)
                    .font(.caption.weight(.bold))
                    .foregroundStyle(.primary)
                Text(detail)
                    .font(.caption)
                    .foregroundStyle(.secondary)
                    .fixedSize(horizontal: false, vertical: true)
            }
        }
    }
}
