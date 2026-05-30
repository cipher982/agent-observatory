import SwiftUI

// The Observatory's beautiful realtime face. A NavigationSplitView with:
//  • a hero background (animated mesh gradient) extended under the chrome
//  • a sidebar of live agent sessions (glass cards)
//  • a detail pane that is, by default, the LIVE ACTIVITY FEED — in-flight LLM
//    requests blooming in via Liquid Glass .materialize transitions as they
//    cross the wire in realtime.
struct ContentView: View {
    @Environment(EngineClient.self) private var engine
    @State private var selection: SessionView.ID?

    var body: some View {
        NavigationSplitView {
            sidebar
                .navigationSplitViewColumnWidth(min: 300, ideal: 340)
        } detail: {
            ZStack {
                HeroBackground(pulse: engine.pulse, live: engine.streamConnected)
                detail
            }
        }
        .navigationTitle("Agent Observatory")
    }

    // MARK: sidebar — live agent sessions

    private var sidebar: some View {
        Group {
            switch engine.state {
            case .starting:
                ContentUnavailableView { Label("Starting engine…", systemImage: "gearshape.2") }
            case .failed(let msg):
                ContentUnavailableView { Label("Engine unavailable", systemImage: "bolt.horizontal.circle") }
                    description: { Text(msg) }
            case .running:
                List(selection: $selection) {
                    Section {
                        LiveStatusRow(connected: engine.streamConnected, eventCount: engine.liveEvents.count)
                            .listRowSeparator(.hidden)
                    }
                    Section("Agents") {
                        ForEach(engine.sessions) { v in
                            SessionRow(view: v).tag(v.id)
                                .listRowSeparator(.hidden)
                        }
                    }
                }
                .listStyle(.sidebar)
                .scrollEdgeEffectStyle(.soft, for: .top)
            }
        }
        .safeAreaInset(edge: .bottom) {
            if let u = engine.lastUpdated {
                Text("\(engine.sessions.count) agents · updated \(u.formatted(date: .omitted, time: .standard))")
                    .font(.caption2).foregroundStyle(.secondary)
                    .frame(maxWidth: .infinity).padding(8)
            }
        }
    }

    // MARK: detail — live feed by default, session detail when one is selected

    @ViewBuilder private var detail: some View {
        if let id = selection, let v = engine.sessions.first(where: { $0.id == id }) {
            SessionDetail(view: v)
        } else {
            LiveFeedView()
        }
    }
}

// A small glass status row showing the live stream heartbeat.
struct LiveStatusRow: View {
    let connected: Bool
    let eventCount: Int
    @State private var breathe = false

    var body: some View {
        HStack(spacing: 10) {
            Circle()
                .fill(connected ? Color.green : Color.gray)
                .frame(width: 10, height: 10)
                .shadow(color: connected ? .green : .clear, radius: breathe ? 6 : 2)
                .scaleEffect(breathe ? 1.25 : 1.0)
                .animation(.easeInOut(duration: 1.1).repeatForever(autoreverses: true), value: breathe)
            VStack(alignment: .leading, spacing: 1) {
                Text(connected ? "LIVE" : "connecting…")
                    .font(.caption.weight(.bold))
                    .foregroundStyle(connected ? .green : .secondary)
                Text("\(eventCount) wire events").font(.caption2).foregroundStyle(.secondary)
            }
            Spacer()
            Image(systemName: "antenna.radiowaves.left.and.right")
                .foregroundStyle(connected ? .green : .secondary)
        }
        .padding(.horizontal, 12).padding(.vertical, 10)
        .glassEffect(.regular.tint((connected ? Color.green : Color.gray).opacity(0.35)),
                     in: RoundedRectangle(cornerRadius: 14, style: .continuous))
        .onAppear { breathe = connected }
        .onChange(of: connected) { _, c in breathe = c }
    }
}

// Animated mesh-gradient hero, extended under the window chrome, that subtly
// reacts to live activity (a brief brighten on each new wire event).
struct HeroBackground: View {
    let pulse: Int
    let live: Bool
    @State private var t: CGFloat = 0
    @State private var flash = false

    private var points: [SIMD2<Float>] {
        let cx = Float(0.5 + 0.18 * sin(t))
        let cy = Float(0.5 + 0.12 * cos(t))
        return [
            SIMD2(0, 0), SIMD2(0.5, 0), SIMD2(1, 0),
            SIMD2(0, 0.5), SIMD2(cx, cy), SIMD2(1, 0.5),
            SIMD2(0, 1), SIMD2(0.5, 1), SIMD2(1, 1),
        ]
    }

    private var colors: [Color] {
        let center: Color = flash ? .cyan.opacity(0.7) : .blue.opacity(0.5)
        return [
            .black, .indigo.opacity(0.7), .black,
            .purple.opacity(0.55), center, .indigo.opacity(0.6),
            .black, .purple.opacity(0.45), .black,
        ]
    }

    var body: some View {
        MeshGradient(width: 3, height: 3, points: points, colors: colors)
            .ignoresSafeArea()
        .backgroundExtensionEffect()
        .overlay(.black.opacity(0.35))
        .onAppear {
            withAnimation(.easeInOut(duration: 8).repeatForever(autoreverses: true)) { t = .pi * 2 }
        }
        .onChange(of: pulse) { _, _ in
            withAnimation(.easeOut(duration: 0.25)) { flash = true }
            withAnimation(.easeIn(duration: 1.2).delay(0.25)) { flash = false }
        }
    }
}
