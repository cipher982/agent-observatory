import SwiftUI

// The live activity feed: each in-flight LLM request blooms in as a Liquid Glass
// card the instant it crosses the wire. Uses GlassEffectContainer + .materialize
// so cards gel in without fighting matched-geometry across a churning list.
struct LiveFeedView: View {
    @Environment(EngineClient.self) private var engine
    @Namespace private var glassNS

    var body: some View {
        ScrollView {
            GlassEffectContainer(spacing: 20) {
                LazyVStack(spacing: 14) {
                    if engine.liveEvents.isEmpty {
                        emptyState
                    } else {
                        ForEach(engine.liveEvents) { ev in
                            LiveRequestCard(event: ev)
                                .glassEffectID(ev.id, in: glassNS)
                                .transition(.opacity.combined(with: .move(edge: .top)))
                        }
                    }
                }
                .padding(24)
            }
        }
        .glassEffectTransition(.materialize)
        .animation(.spring(response: 0.45, dampingFraction: 0.85), value: engine.liveEvents.count)
        .scrollEdgeEffectStyle(.soft, for: .top)
        .safeAreaBar(edge: .top) { feedHeader }
    }

    private var feedHeader: some View {
        HStack(spacing: 10) {
            Image(systemName: "waveform.path.ecg")
                .foregroundStyle(.cyan)
            VStack(alignment: .leading, spacing: 1) {
                Text("Live wire activity").font(.headline)
                Text("in-flight LLM requests, captured as they leave the machine")
                    .font(.caption).foregroundStyle(.secondary)
            }
            Spacer()
            if engine.streamConnected {
                Label("LIVE", systemImage: "dot.radiowaves.left.and.right")
                    .font(.caption.weight(.bold))
                    .foregroundStyle(.green)
            }
        }
        .padding(.horizontal, 16).padding(.vertical, 12)
    }

    private var emptyState: some View {
        VStack(spacing: 14) {
            Image(systemName: "antenna.radiowaves.left.and.right")
                .font(.system(size: 44)).foregroundStyle(.secondary)
            Text("Waiting for agent requests…").font(.title3.weight(.semibold))
            Text("Launch an agent through the observatory proxy and its outbound\nLLM requests will stream in here in realtime.")
                .font(.callout).foregroundStyle(.secondary)
                .multilineTextAlignment(.center)
            if !engine.proxyCommand.isEmpty {
                Text(engine.proxyCommand)
                    .font(.caption.monospaced())
                    .padding(12)
                    .frame(maxWidth: 560)
                    .glassEffect(.regular.tint(.blue.opacity(0.18)), in: RoundedRectangle(cornerRadius: 12, style: .continuous))
                    .textSelection(.enabled)
            }
        }
        .frame(maxWidth: .infinity)
        .padding(.top, 80)
    }
}

// One captured request, as a glass card. Tint encodes runtime; a witness badge
// encodes whether the AGENTS.md doctrine was present in the outbound body.
struct LiveRequestCard: View {
    let event: LiveEvent
    @State private var appeared = false

    var body: some View {
        HStack(alignment: .top, spacing: 14) {
            VStack(spacing: 4) {
                Image(systemName: runtimeIcon).font(.title2)
                Text(event.runtime).font(.caption2.weight(.semibold))
            }
            .frame(width: 56)
            .foregroundStyle(.white)

            VStack(alignment: .leading, spacing: 6) {
                HStack(spacing: 8) {
                    Text(event.endpoint).font(.headline.monospaced())
                    Spacer()
                    Text(timeShort).font(.caption).foregroundStyle(.secondary)
                }
                Text(event.host).font(.caption).foregroundStyle(.secondary).lineLimit(1).truncationMode(.middle)
                HStack(spacing: 10) {
                    metric("\(event.systemChars)", "chars", .blue)
                    metric("\(event.toolCount)", "tools", .mint)
                    doctrineBadge
                }
                if let tools = event.toolNames, !tools.isEmpty {
                    Text(tools.prefix(8).joined(separator: " · "))
                        .font(.caption2.monospaced()).foregroundStyle(.secondary)
                        .lineLimit(2)
                }
            }
        }
        .padding(16)
        .frame(maxWidth: .infinity, alignment: .leading)
        .glassEffect(.regular.tint(runtimeColor.opacity(0.5)).interactive(),
                     in: RoundedRectangle(cornerRadius: 18, style: .continuous))
        .scaleEffect(appeared ? 1 : 0.96)
        .opacity(appeared ? 1 : 0)
        .onAppear { withAnimation(.spring(response: 0.4, dampingFraction: 0.8)) { appeared = true } }
    }

    private func metric(_ value: String, _ label: String, _ color: Color) -> some View {
        HStack(spacing: 3) {
            Text(value).font(.caption.weight(.bold)).foregroundStyle(color)
            Text(label).font(.caption2).foregroundStyle(.secondary)
        }
    }

    private var doctrineBadge: some View {
        HStack(spacing: 3) {
            Image(systemName: event.agentsMarker ? "checkmark.seal.fill" : "exclamationmark.triangle.fill")
            Text(event.agentsMarker ? "AGENTS.md ✓" : "no doctrine")
        }
        .font(.caption2.weight(.semibold))
        .foregroundStyle(event.agentsMarker ? .green : .orange)
    }

    private var runtimeIcon: String {
        switch event.runtime {
        case "claude": return "brain.head.profile"
        case "codex": return "chevron.left.forwardslash.chevron.right"
        default: return "cpu"
        }
    }
    private var runtimeColor: Color {
        switch event.runtime {
        case "claude": return .purple
        case "codex": return .green
        default: return .gray
        }
    }
    private var timeShort: String {
        guard let d = ISO8601DateFormatter().date(from: event.at) else { return "" }
        return d.formatted(date: .omitted, time: .standard)
    }
}
