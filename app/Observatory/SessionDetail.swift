import SwiftUI

// The detail pane: the fact-level evidence for one agent session, rendered as
// witness marks with their confidence tier. Conflicts and drift are called out.
struct SessionDetail: View {
    let view: SessionView

    var body: some View {
        ScrollView {
            VStack(alignment: .leading, spacing: 20) {
                header
                if !conflicts.isEmpty { conflictCard }
                if !drift.isEmpty { driftCard }
                factsCard
                sourcesCard
            }
            .padding(24)
            .frame(maxWidth: .infinity, alignment: .leading)
        }
        .navigationTitle(view.workspace.isEmpty ? view.session.Runtime : view.workspace)
        .navigationSubtitle(view.shortCWD)
    }

    private var conflicts: [FactResult] { view.facts.filter { $0.statusEnum == .conflict } }
    private var drift: [FactResult] { view.facts.filter { $0.statusEnum == .missingExpected } }

    private var header: some View {
        HStack(spacing: 12) {
            VStack(alignment: .leading, spacing: 4) {
                HStack {
                    Text(view.session.Runtime).font(.title2.bold())
                    levelBadge
                }
                Text(view.shortCWD).font(.callout).foregroundStyle(.secondary)
                if !view.session.Version.isEmpty {
                    Text("v\(view.session.Version) · \(view.session.RecordCount) records")
                        .font(.caption).foregroundStyle(.tertiary)
                }
            }
            Spacer()
        }
    }

    private var levelBadge: some View {
        let lvl = view.summaryLevel
        let color: Color = lvl == "verified" ? .green : (lvl == "observed" ? .blue : .gray)
        return Text(lvl.uppercased())
            .font(.caption.bold())
            .padding(.horizontal, 8).padding(.vertical, 3)
            .background(color, in: Capsule())
            .foregroundStyle(.white)
    }

    // CONFLICT — the loudest alarm: a Complete-coverage transcript/wire disagreement.
    private var conflictCard: some View {
        VStack(alignment: .leading, spacing: 10) {
            sectionTitle("CONFLICT — transcript disagrees with the wire", systemImage: "bolt.trianglebadge.exclamationmark.fill")
                .foregroundStyle(.purple)
            ForEach(conflicts) { f in
                Text(f.displayName).font(.callout.monospaced())
            }
        }
        .padding(16).frame(maxWidth: .infinity, alignment: .leading)
        .glassEffect(.regular.tint(.purple.opacity(0.22)), in: RoundedRectangle(cornerRadius: 18, style: .continuous))
    }

    private var driftCard: some View {
        VStack(alignment: .leading, spacing: 10) {
            sectionTitle("drift — expected but missing from the assembled catalog", systemImage: "exclamationmark.triangle.fill")
                .foregroundStyle(.red)
            tagWrap(drift.map { $0.key.name }, tint: .red)
        }
        .padding(16).frame(maxWidth: .infinity, alignment: .leading)
        .glassEffect(.regular.tint(.red.opacity(0.18)), in: RoundedRectangle(cornerRadius: 18, style: .continuous))
    }

    private var factsCard: some View {
        GlassEffectContainer(spacing: 12) {
            VStack(alignment: .leading, spacing: 8) {
                sectionTitle("facts (\(view.facts.count))", systemImage: "checkmark.seal")
                ForEach(view.facts) { f in
                    HStack(alignment: .top, spacing: 10) {
                        Image(systemName: f.statusEnum.glyph)
                            .foregroundStyle(color(for: f.statusEnum))
                            .font(.body.weight(.semibold))
                            .frame(width: 18)
                        VStack(alignment: .leading, spacing: 1) {
                            Text(f.displayName).font(.callout.weight(.medium))
                            Text(f.statusEnum.label).font(.caption).foregroundStyle(.secondary)
                        }
                    }
                }
            }
            .padding(16).frame(maxWidth: .infinity, alignment: .leading)
            .glassEffect(.regular, in: RoundedRectangle(cornerRadius: 18, style: .continuous))
        }
    }

    private var sourcesCard: some View {
        VStack(alignment: .leading, spacing: 8) {
            sectionTitle("evidence sources", systemImage: "antenna.radiowaves.left.and.right")
            ForEach(view.sourceStatus ?? []) { s in
                HStack(spacing: 8) {
                    Image(systemName: s.available ? "checkmark.circle.fill" : "minus.circle")
                        .foregroundStyle(s.available ? .green : .secondary)
                    Text(s.source).font(.callout.weight(.medium))
                    if let r = s.reason, !r.isEmpty {
                        Text(r).font(.caption).foregroundStyle(.secondary)
                    }
                }
            }
        }
        .padding(16).frame(maxWidth: .infinity, alignment: .leading)
        .glassEffect(.regular, in: RoundedRectangle(cornerRadius: 18, style: .continuous))
    }

    private func sectionTitle(_ text: String, systemImage: String) -> some View {
        Label(text, systemImage: systemImage)
            .font(.subheadline.weight(.semibold))
            .textCase(.uppercase)
            .foregroundStyle(.secondary)
    }

    private func tagWrap(_ items: [String], tint: Color) -> some View {
        FlowLayout(spacing: 6) {
            ForEach(items, id: \.self) { item in
                Text(item)
                    .font(.caption.monospaced())
                    .padding(.horizontal, 8).padding(.vertical, 4)
                    .background(tint.opacity(0.12), in: RoundedRectangle(cornerRadius: 6))
                    .overlay(RoundedRectangle(cornerRadius: 6).strokeBorder(tint.opacity(0.3)))
                    .foregroundStyle(tint)
            }
        }
    }

    private func color(for s: FactStatus) -> Color {
        switch s {
        case .expectedVerified, .expectedObserved: return .green
        case .missingExpected: return .red
        case .conflict: return .purple
        default: return .secondary
        }
    }
}

// Wrapping flow layout for tag chips.
struct FlowLayout: Layout {
    var spacing: CGFloat = 6
    func sizeThatFits(proposal: ProposedViewSize, subviews: Subviews, cache: inout ()) -> CGSize {
        let maxWidth = proposal.width ?? .infinity
        var x: CGFloat = 0, y: CGFloat = 0, rowH: CGFloat = 0
        for s in subviews {
            let sz = s.sizeThatFits(.unspecified)
            if x + sz.width > maxWidth, x > 0 { x = 0; y += rowH + spacing; rowH = 0 }
            x += sz.width + spacing; rowH = max(rowH, sz.height)
        }
        return CGSize(width: maxWidth == .infinity ? x : maxWidth, height: y + rowH)
    }
    func placeSubviews(in bounds: CGRect, proposal: ProposedViewSize, subviews: Subviews, cache: inout ()) {
        var x = bounds.minX, y = bounds.minY, rowH: CGFloat = 0
        for s in subviews {
            let sz = s.sizeThatFits(.unspecified)
            if x + sz.width > bounds.minX + bounds.width, x > bounds.minX { x = bounds.minX; y += rowH + spacing; rowH = 0 }
            s.place(at: CGPoint(x: x, y: y), proposal: ProposedViewSize(sz))
            x += sz.width + spacing; rowH = max(rowH, sz.height)
        }
    }
}
