import SwiftUI
import UsModel
import UsDesignSystem

public struct HeatmapPoint: Identifiable {
    public let id = UUID()
    public let x: CGFloat
    public let y: CGFloat
}

public struct StoryHeatmapStickerView: View {
    public let prompt: String
    public let onTapped: (CGPoint) -> Void

    @State private var tapPoints: [HeatmapPoint] = [
        HeatmapPoint(x: 80, y: 50),
        HeatmapPoint(x: 85, y: 52),
        HeatmapPoint(x: 180, y: 70),
        HeatmapPoint(x: 175, y: 68),
        HeatmapPoint(x: 190, y: 74)
    ]

    public init(
        prompt: String = "Tap where you think the next Bangalore unicorn is! 🦄",
        onTapped: @escaping (CGPoint) -> Void = { _ in }
    ) {
        self.prompt = prompt
        self.onTapped = onTapped
    }

    public var body: some View {
        VStack(spacing: 10) {
            Text(prompt)
                .font(.system(size: 13, weight: .bold))
                .foregroundColor(.white)
                .multilineTextAlignment(.center)
                .padding(.horizontal, 12)

            ZStack {
                RoundedRectangle(cornerRadius: 14)
                    .fill(Color.black.opacity(0.75))
                    .overlay(RoundedRectangle(cornerRadius: 14).stroke(Color.cyan.opacity(0.4), lineWidth: 1))

                // Render glowing heat points
                Canvas { context, size in
                    for pt in tapPoints {
                        let rect = CGRect(x: pt.x - 15, y: pt.y - 15, width: 30, height: 30)
                        context.fill(
                            Circle().path(in: rect),
                            with: .color(Color.cyan.opacity(0.6))
                        )
                        let centerRect = CGRect(x: pt.x - 6, y: pt.y - 6, width: 12, height: 12)
                        context.fill(
                            Circle().path(in: centerRect),
                            with: .color(Color.white)
                        )
                    }
                }

                Text("Tap anywhere to vote • \(tapPoints.count) votes")
                    .font(.system(size: 10, weight: .semibold))
                    .foregroundColor(Color.white.opacity(0.6))
                    .padding(8)
                    .frame(maxHeight: .infinity, alignment: .bottom)
            }
            .frame(height: 120)
            .contentShape(Rectangle())
            .onTapGesture { location in
                HapticManager.shared.trigger(.selection)
                tapPoints.append(HeatmapPoint(x: location.x, y: location.y))
                onTapped(location)
            }
        }
        .padding(14)
        .background(
            LinearGradient(
                colors: [Color(red: 0x10/255.0, green: 0x1C/255.0, blue: 0x38/255.0), Color(red: 0x14/255.0, green: 0x14/255.0, blue: 0x22/255.0)],
                startPoint: .topLeading,
                endPoint: .bottomTrailing
            )
        )
        .clipShape(RoundedRectangle(cornerRadius: 18))
        .overlay(RoundedRectangle(cornerRadius: 18).stroke(Color.cyan.opacity(0.3), lineWidth: 1.5))
        .frame(width: 290)
    }
}
