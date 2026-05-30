import SwiftUI

// One row in the agent list: runtime chip, workspace, cwd, the evidence LEVEL
// badge (EXPECTED/OBSERVED/VERIFIED), and a compact witness-mark summary.
struct SessionRow: View {
    let view: SessionView

    var body: some View {
        HStack(spacing: 12) {
            runtimeChip
            VStack(alignment: .leading, spacing: 2) {
                HStack(spacing: 6) {
                    Text(view.workspace.isEmpty ? "—" : view.workspace).font(.headline)
                    levelBadge
                }
                Text(view.shortCWD)
                    .font(.caption).foregroundStyle(.secondary)
                    .lineLimit(1).truncationMode(.middle)
            }
            Spacer()
            marks
        }
        .padding(.vertical, 4)
    }

    private var runtimeChip: some View {
        Text(view.session.Runtime)
            .font(.caption.weight(.semibold))
            .padding(.horizontal, 10).padding(.vertical, 5)
            .glassEffect(.regular.tint(runtimeColor.opacity(0.55)).interactive(), in: Capsule())
    }

    private var levelBadge: some View {
        let lvl = view.summaryLevel
        let color: Color = lvl == "verified" ? .green : (lvl == "observed" ? .blue : .gray)
        return Text(lvl.uppercased())
            .font(.system(size: 9, weight: .bold))
            .padding(.horizontal, 6).padding(.vertical, 2)
            .background(color, in: Capsule())
            .foregroundStyle(.white)
    }

    private var marks: some View {
        let c = view.markCounts
        return HStack(spacing: 6) {
            if c.good > 0 { pill(c.good, "checkmark", .green) }
            if c.drift > 0 { pill(c.drift, "exclamationmark.triangle.fill", .red) }
            if c.conflict > 0 { pill(c.conflict, "bolt.trianglebadge.exclamationmark.fill", .purple) }
            if c.gap > 0 { pill(c.gap, "minus", .secondary) }
        }
    }

    private func pill(_ n: Int, _ symbol: String, _ color: Color) -> some View {
        HStack(spacing: 2) {
            Image(systemName: symbol)
            Text("\(n)")
        }
        .font(.caption2.weight(.medium))
        .foregroundStyle(color)
    }

    private var runtimeColor: Color {
        switch view.session.Runtime {
        case "claude": return .purple
        case "codex": return .green
        case "antigravity": return .blue
        default: return .gray
        }
    }
}
