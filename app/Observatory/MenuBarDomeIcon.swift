import SwiftUI

struct MenuBarDomeIcon: View {
    var degraded: Bool

    var body: some View {
        ZStack(alignment: .topTrailing) {
            DomeMark()
                .fill(.primary, style: FillStyle(eoFill: true))
                .frame(width: 20, height: 20)

            if degraded {
                Circle()
                    .fill(Color.red)
                    .frame(width: 6, height: 6)
                    .overlay(
                        Circle()
                            .stroke(Color.black.opacity(0.42), lineWidth: 0.8)
                    )
                    .offset(x: 1, y: -1)
            }
        }
        .frame(width: 24, height: 22)
        .accessibilityLabel(degraded ? "Agent Observatory reconnecting" : "Agent Observatory connected")
    }
}

private struct DomeMark: Shape {
    func path(in rect: CGRect) -> Path {
        let sx = rect.width / 32
        let sy = rect.height / 32

        func point(_ x: CGFloat, _ y: CGFloat) -> CGPoint {
            CGPoint(x: rect.minX + x * sx, y: rect.minY + y * sy)
        }

        var path = Path()

        // Detached foundation. The gap between dome and base is intentional: it
        // makes the mark read as an observatory rather than a helmet brim.
        path.move(to: point(4.5, 24))
        path.addLine(to: point(27.5, 24))
        path.addLine(to: point(27.5, 26.4))
        path.addLine(to: point(4.5, 26.4))
        path.closeSubpath()

        // Dome.
        path.move(to: point(7, 22.2))
        path.addCurve(
            to: point(16, 9.8),
            control1: point(7, 14),
            control2: point(11, 9.8)
        )
        path.addCurve(
            to: point(25, 22.2),
            control1: point(21, 9.8),
            control2: point(25, 14)
        )
        path.addLine(to: point(7, 22.2))
        path.closeSubpath()

        // Aperture cutout.
        path.move(to: point(16.5, 10.9))
        path.addCurve(
            to: point(21.2, 17.8),
            control1: point(19, 11.6),
            control2: point(20.6, 13.9)
        )
        path.addLine(to: point(15.6, 19))
        path.addLine(to: point(16.5, 10.9))
        path.closeSubpath()

        return path
    }
}
