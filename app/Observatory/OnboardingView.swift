import AppKit
import SwiftUI

struct OnboardingView: View {
    @Environment(EngineClient.self) private var engine

    let onExploreDemo: () -> Void
    let onUseLive: () -> Void

    @State private var showSetup = false
    @State private var commandCopied = false
    @State private var uninstallCopied = false

    var body: some View {
        ScrollView {
            VStack(alignment: .leading, spacing: 24) {
                firstRunHero
                activationStrip
                setupPanel
            }
            .padding(.horizontal, 32)
            .padding(.vertical, 28)
            .frame(maxWidth: 1080)
            .frame(maxWidth: .infinity)
        }
        .scrollEdgeEffectStyle(.soft, for: .top)
        .task { await engine.checkInstallStatus() }
    }

    private var firstRunHero: some View {
        ViewThatFits(in: .horizontal) {
            HStack(alignment: .center, spacing: 28) {
                heroCopy.frame(minWidth: 360, maxWidth: 520, alignment: .leading)
                heroPreview.frame(maxWidth: 420)
            }

            VStack(alignment: .leading, spacing: 24) {
                heroCopy
                heroPreview
            }
        }
        .padding(28)
        .glassEffect(.regular.tint(Color.black.opacity(0.22)),
                     in: RoundedRectangle(cornerRadius: 18, style: .continuous))
    }

    private var heroCopy: some View {
        VStack(alignment: .leading, spacing: 18) {
            Label("Local-first setup · sample data already running", systemImage: "lock.shield")
                .font(.caption.weight(.semibold))
                .foregroundStyle(.mint)

            Text("Watch context become evidence.")
                .font(.system(size: 48, weight: .bold, design: .rounded))
                .lineLimit(2)
                .minimumScaleFactor(0.72)
                .foregroundStyle(.white)

            Text("Observatory starts with a live demo feed so the value is visible before any proxy or trust setup. When you are ready, switch to live capture and verify what your own agents send.")
                .font(.title3)
                .foregroundStyle(.secondary)
                .fixedSize(horizontal: false, vertical: true)

            HStack(spacing: 12) {
                Button {
                    onExploreDemo()
                } label: {
                    Label("Explore Demo Feed", systemImage: "play.circle.fill")
                }
                .buttonStyle(.borderedProminent)
                .controlSize(.large)

                Button {
                    withAnimation(.spring(response: 0.35, dampingFraction: 0.86)) {
                        showSetup = true
                    }
                } label: {
                    Label("Set Up Live Capture", systemImage: "dot.radiowaves.left.and.right")
                }
                .buttonStyle(.bordered)
                .controlSize(.large)
            }

            HStack(spacing: 14) {
                trustChip("No account", "person.crop.circle.badge.checkmark")
                trustChip("Loopback only", "network")
                trustChip("Raw prompts stay off disk", "doc.badge.ellipsis")
            }
        }
    }

    private var heroPreview: some View {
        VStack(alignment: .leading, spacing: 14) {
            HStack {
                Label("First useful moment", systemImage: "sparkles")
                    .font(.headline)
                Spacer()
                Text("SAMPLE")
                    .font(.caption.weight(.bold))
                    .foregroundStyle(.mint)
            }

            RequestPreviewLine(runtime: "codex", endpoint: "openai/responses", tools: "5 tools", color: .green)
            RequestPreviewLine(runtime: "claude", endpoint: "bedrock/invoke", tools: "8 tools", color: .purple)

            Divider().opacity(0.25)

            VStack(alignment: .leading, spacing: 10) {
                EvidenceStep(label: "Expected", detail: "AGENTS.md, skills, tools", symbol: "doc.text.magnifyingglass", color: .cyan)
                EvidenceStep(label: "Observed", detail: "transcripts confirm what appeared", symbol: "checkmark.circle.fill", color: .blue)
                EvidenceStep(label: "Verified", detail: "outbound request facts captured", symbol: "checkmark.seal.fill", color: .green)
            }
        }
        .padding(18)
        .frame(maxWidth: .infinity, alignment: .leading)
        .glassEffect(.regular.tint(Color.cyan.opacity(0.16)).interactive(),
                     in: RoundedRectangle(cornerRadius: 14, style: .continuous))
    }

    private var activationStrip: some View {
        Grid(horizontalSpacing: 12, verticalSpacing: 12) {
            GridRow {
                OnboardingMetric(value: "\(max(engine.sessions.count, 3))", label: "sample sessions", symbol: "sidebar.leading")
                OnboardingMetric(value: "\(max(engine.liveEvents.count, 2))", label: "sample requests", symbol: "waveform.path.ecg")
                OnboardingMetric(value: engine.mode.rawValue, label: "current mode", symbol: "switch.2")
            }
        }
    }

    @ViewBuilder private var setupPanel: some View {
        if showSetup {
            VStack(alignment: .leading, spacing: 16) {
                HStack(alignment: .firstTextBaseline) {
                    VStack(alignment: .leading, spacing: 4) {
                        Text("Live capture setup")
                            .font(.title2.weight(.bold))
                        Text("Install once, restart your agent, then keep using Claude, Codex, or other supported runtimes normally.")
                            .foregroundStyle(.secondary)
                    }
                    Spacer()
                    Image(systemName: "shield.lefthalf.filled")
                        .font(.title)
                        .foregroundStyle(.green)
                }

                TrustBoundaryPanel()

                if let warning = engine.helperLocationWarning {
                    Label(warning, systemImage: "exclamationmark.triangle.fill")
                        .font(.caption.weight(.semibold))
                        .foregroundStyle(.orange)
                        .fixedSize(horizontal: false, vertical: true)
                }

                VStack(alignment: .leading, spacing: 10) {
                    ChecklistRow(done: true, title: "Start with demo evidence", detail: "You already saw the request stream working.")
                    ChecklistRow(done: true, title: "Understand the trust boundary", detail: "Only known LLM provider hosts are inspected; unrelated hosts tunnel opaque.")
                    ChecklistRow(done: engine.installReady, title: "Run the local install", detail: installStepDetail)
                }

                Text(engine.installStatusText)
                    .font(.caption.weight(.medium))
                    .foregroundStyle(installStatusColor)

                VStack(alignment: .leading, spacing: 10) {
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

                        Button {
                            copyInstallCommand()
                        } label: {
                            Label(commandCopied ? "Copied" : "Copy Install", systemImage: commandCopied ? "checkmark" : "doc.on.doc")
                        }
                        .buttonStyle(.bordered)
                        .disabled(!engine.installCommandAvailable)
                    }

                    HStack(spacing: 10) {
                        Button {
                            Task { await engine.checkInstallStatus() }
                        } label: {
                            Label("Check Status", systemImage: "arrow.clockwise")
                        }
                        .buttonStyle(.bordered)

                        Button {
                            onUseLive()
                        } label: {
                            Label(engine.installReady ? "Continue Live" : "Install First", systemImage: "arrow.right.circle.fill")
                        }
                        .buttonStyle(.borderedProminent)
                        .disabled(!engine.installReady)
                    }
                }

                HStack(spacing: 10) {
                    Label("Reset", systemImage: "arrow.counterclockwise")
                        .font(.caption.weight(.semibold))
                        .foregroundStyle(.secondary)

                    Text(engine.uninstallCommand)
                        .font(.caption.monospaced())
                        .lineLimit(1)
                        .truncationMode(.middle)
                        .padding(.horizontal, 10)
                        .padding(.vertical, 8)
                        .frame(maxWidth: .infinity, alignment: .leading)
                        .background(.black.opacity(0.16), in: RoundedRectangle(cornerRadius: 8, style: .continuous))
                        .textSelection(.enabled)

                    Button {
                        copyUninstallCommand()
                    } label: {
                        Label(uninstallCopied ? "Copied" : "Copy Reset", systemImage: uninstallCopied ? "checkmark" : "trash")
                    }
                    .buttonStyle(.bordered)
                    .disabled(!engine.installCommandAvailable)
                }
            }
            .padding(20)
            .glassEffect(.regular.tint(Color.green.opacity(0.16)),
                         in: RoundedRectangle(cornerRadius: 14, style: .continuous))
            .transition(.opacity.combined(with: .move(edge: .top)))
        }
    }

    private func trustChip(_ title: String, _ symbol: String) -> some View {
        Label(title, systemImage: symbol)
            .font(.caption.weight(.medium))
            .foregroundStyle(.primary)
            .padding(.horizontal, 10)
            .padding(.vertical, 6)
            .background(.black.opacity(0.18), in: Capsule())
    }

    private var installStepDetail: String {
        if engine.installReady {
            return "Installed. Newly launched agents can be captured in Live mode."
        }
        return "Copies the bundled helper path, installs a LaunchAgent, sets proxy/trust env, and can be fully reversed with uninstall."
    }

    private var installStatusColor: Color {
        switch engine.installState {
        case .installed: return .green
        case .partial: return .orange
        case .missing: return .secondary
        case .unknown: return .secondary
        case .unavailable: return .red
        }
    }

    private func copyInstallCommand() {
        NSPasteboard.general.clearContents()
        NSPasteboard.general.setString(engine.installCommand, forType: .string)
        commandCopied = true
        Task {
            try? await Task.sleep(for: .seconds(1.2))
            await MainActor.run { commandCopied = false }
        }
    }

    private func copyUninstallCommand() {
        NSPasteboard.general.clearContents()
        NSPasteboard.general.setString(engine.uninstallCommand, forType: .string)
        uninstallCopied = true
        Task {
            try? await Task.sleep(for: .seconds(1.2))
            await MainActor.run { uninstallCopied = false }
        }
    }
}

private struct RequestPreviewLine: View {
    let runtime: String
    let endpoint: String
    let tools: String
    let color: Color

    var body: some View {
        HStack(spacing: 12) {
            Circle()
                .fill(color)
                .frame(width: 10, height: 10)
                .shadow(color: color.opacity(0.8), radius: 6)
            VStack(alignment: .leading, spacing: 2) {
                Text(endpoint)
                    .font(.callout.monospaced().weight(.semibold))
                Text(runtime)
                    .font(.caption)
                    .foregroundStyle(.secondary)
            }
            Spacer()
            Text(tools)
                .font(.caption.weight(.semibold))
                .foregroundStyle(color)
        }
        .padding(12)
        .background(.white.opacity(0.06), in: RoundedRectangle(cornerRadius: 8, style: .continuous))
    }
}

private struct EvidenceStep: View {
    let label: String
    let detail: String
    let symbol: String
    let color: Color

    var body: some View {
        HStack(spacing: 10) {
            Image(systemName: symbol)
                .frame(width: 24)
                .foregroundStyle(color)
            VStack(alignment: .leading, spacing: 1) {
                Text(label)
                    .font(.callout.weight(.semibold))
                Text(detail)
                    .font(.caption)
                    .foregroundStyle(.secondary)
            }
        }
    }
}

private struct TrustBoundaryPanel: View {
    var body: some View {
        VStack(alignment: .leading, spacing: 12) {
            Label("What live capture changes", systemImage: "shield.lefthalf.filled")
                .font(.headline)
                .foregroundStyle(.green)

            Text("Verified capture uses a local CA so agent processes can trust Observatory's loopback proxy for known LLM provider hosts. The proxy extracts derived request facts, not stored raw prompt bodies.")
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
                    TrustFact(symbol: "trash", title: "Reversible", detail: "Reset removes env, LaunchAgent, and CA state")
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

private struct OnboardingMetric: View {
    let value: String
    let label: String
    let symbol: String

    var body: some View {
        HStack(spacing: 12) {
            Image(systemName: symbol)
                .font(.title3)
                .foregroundStyle(.mint)
                .frame(width: 28)
            VStack(alignment: .leading, spacing: 2) {
                Text(value)
                    .font(.headline)
                    .lineLimit(1)
                Text(label)
                    .font(.caption)
                    .foregroundStyle(.secondary)
            }
            Spacer(minLength: 0)
        }
        .padding(14)
        .frame(maxWidth: .infinity, minHeight: 72, alignment: .leading)
        .glassEffect(.regular.tint(Color.black.opacity(0.18)),
                     in: RoundedRectangle(cornerRadius: 12, style: .continuous))
    }
}

private struct ChecklistRow: View {
    let done: Bool
    let title: String
    let detail: String

    var body: some View {
        HStack(alignment: .top, spacing: 10) {
            Image(systemName: done ? "checkmark.circle.fill" : "circle")
                .foregroundStyle(done ? .green : .secondary)
                .padding(.top, 1)
            VStack(alignment: .leading, spacing: 2) {
                Text(title)
                    .font(.callout.weight(.semibold))
                Text(detail)
                    .font(.caption)
                    .foregroundStyle(.secondary)
            }
        }
    }
}
