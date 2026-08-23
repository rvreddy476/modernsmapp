import SwiftUI
import UsModel
import UsDesignSystem

public struct EmojiSliderStickerView: View {
    public let question: String
    public let emoji: String
    public let onAnswer: (Double) -> Void

    @State private var sliderValue: Double = 0.5
    @State private var hasVoted: Bool = false

    public init(
        question: String = "How hype is this track?",
        emoji: String = "🔥",
        onAnswer: @escaping (Double) -> Void = { _ in }
    ) {
        self.question = question
        self.emoji = emoji
        self.onAnswer = onAnswer
    }

    public var body: some View {
        VStack(spacing: 12) {
            Text(question)
                .font(.system(size: 16, weight: .bold))
                .foregroundColor(.white)
                .multilineTextAlignment(.center)

            // Slider Track
            GeometryReader { geo in
                ZStack(alignment: .leading) {
                    // Inactive Track
                    Capsule()
                        .fill(Color.white.opacity(0.3))
                        .frame(height: 12)

                    // Active Fill Track
                    Capsule()
                        .fill(
                            LinearGradient(
                                colors: [Color.orange, Color.red],
                                startPoint: .leading,
                                endPoint: .trailing
                            )
                        )
                        .frame(width: max(12, geo.size.width * CGFloat(sliderValue)), height: 12)

                    // Draggable Emoji Thumb
                    Text(emoji)
                        .font(.system(size: CGFloat(28 + (sliderValue * 20))))
                        .offset(x: max(0, min(geo.size.width - 40, (geo.size.width * CGFloat(sliderValue)) - 20)))
                        .gesture(
                            DragGesture()
                                .onChanged { value in
                                    let newVal = max(0.0, min(1.0, Double(value.location.x / geo.size.width)))
                                    sliderValue = newVal
                                    HapticManager.shared.trigger(.selection)
                                }
                                .onEnded { _ in
                                    hasVoted = true
                                    HapticManager.shared.trigger(.success)
                                    onAnswer(sliderValue)
                                }
                        )
                }
            }
            .frame(height: 50)
            .padding(.horizontal, 16)
        }
        .padding(18)
        .background(Color.black.opacity(0.75))
        .clipShape(RoundedRectangle(cornerRadius: 20))
        .overlay(RoundedRectangle(cornerRadius: 20).stroke(Color.white.opacity(0.2), lineWidth: 1))
        .frame(width: 280)
    }
}
