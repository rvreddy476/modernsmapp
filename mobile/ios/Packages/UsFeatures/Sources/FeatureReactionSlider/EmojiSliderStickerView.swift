import SwiftUI
import UsModel
import UsDesignSystem

public struct EmojiSliderStickerView: View {
    public let question: String
    public let emoji: String
    public let onSlideEnded: (Double) -> Void

    @State private var sliderValue: Double = 0.5
    @State private var hasVoted: Bool = false
    @State private var averageValue: Double = 0.82

    public init(
        question: String = "How hyped are you for this drop? 🔥",
        emoji: String = "🔥",
        onSlideEnded: @escaping (Double) -> Void = { _ in }
    ) {
        self.question = question
        self.emoji = emoji
        self.onSlideEnded = onSlideEnded
    }

    public var body: some View {
        VStack(spacing: 12) {
            Text(question)
                .font(.system(size: 15, weight: .bold))
                .foregroundColor(.white)
                .multilineTextAlignment(.center)
                .padding(.horizontal, 8)

            // Custom Draggable Emoji Slider
            GeometryReader { geo in
                let width = geo.size.width
                ZStack(alignment: .leading) {
                    // Slider Track
                    Capsule()
                        .fill(Color.white.opacity(0.3))
                        .frame(height: 14)

                    // Fill bar
                    Capsule()
                        .fill(
                            LinearGradient(
                                colors: [Color.orange, Color.red],
                                startPoint: .leading,
                                endPoint: .trailing
                            )
                        )
                        .frame(width: max(14, width * CGFloat(sliderValue)), height: 14)

                    // Average needle indicator if voted
                    if hasVoted {
                        Rectangle()
                            .fill(Color.white)
                            .frame(width: 3, height: 22)
                            .offset(x: width * CGFloat(averageValue) - 1.5)
                    }

                    // Draggable Emoji Knob
                    Text(emoji)
                        .font(.system(size: 26 + CGFloat(sliderValue * 14)))
                        .offset(x: min(width - 40, max(0, width * CGFloat(sliderValue) - 20)))
                        .gesture(
                            DragGesture()
                                .onChanged { value in
                                    let progress = min(1.0, max(0.0, Double(value.location.x / width)))
                                    sliderValue = progress
                                    HapticManager.shared.trigger(.selection)
                                }
                                .onEnded { _ in
                                    hasVoted = true
                                    HapticManager.shared.trigger(.success)
                                    onSlideEnded(sliderValue)
                                }
                        )
                }
            }
            .frame(height: 44)
            .padding(.horizontal, 8)

            if hasVoted {
                Text(String(format: "Average Response: %.0f%%", averageValue * 100))
                    .font(.system(size: 11, weight: .bold))
                    .foregroundColor(.white.opacity(0.8))
            }
        }
        .padding(16)
        .background(Color.black.opacity(0.85))
        .clipShape(RoundedRectangle(cornerRadius: 20))
        .overlay(RoundedRectangle(cornerRadius: 20).stroke(Color.white.opacity(0.2), lineWidth: 1))
        .frame(width: 280)
    }
}
