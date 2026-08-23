import SwiftUI
import UsModel
import UsDesignSystem

public struct EmojiPongStickerView: View {
    public let stickerTitle: String
    public let highScore: Int
    public let onScoreSubmitted: (Int) -> Void

    @State private var currentScore: Int = 0
    @State private var isPlaying: Bool = false
    @State private var paddlePosition: CGFloat = 0.0

    public init(
        stickerTitle: String = "Bounce the 🏓 & Beat My Score (42)",
        highScore: Int = 42,
        onScoreSubmitted: @escaping (Int) -> Void = { _ in }
    ) {
        self.stickerTitle = stickerTitle
        self.highScore = highScore
        self.onScoreSubmitted = onScoreSubmitted
    }

    public var body: some View {
        VStack(spacing: 12) {
            // Header
            HStack {
                VStack(alignment: .leading, spacing: 2) {
                    Text(stickerTitle)
                        .font(.system(size: 13, weight: .bold))
                        .foregroundColor(.white)
                    Text("Top Score to Beat: \(highScore) pts")
                        .font(.system(size: 10))
                        .foregroundColor(Color.yellow)
                }

                Spacer()

                Text("SCORE: \(currentScore)")
                    .font(.system(size: 13, weight: .black, design: .monospaced))
                    .foregroundColor(UsColors.onlineGreen)
            }

            // Mini-Game Play Field
            ZStack {
                RoundedRectangle(cornerRadius: 14)
                    .fill(Color.black.opacity(0.85))
                    .overlay(RoundedRectangle(cornerRadius: 14).stroke(Color.white.opacity(0.2), lineWidth: 1))

                if !isPlaying {
                    VStack(spacing: 8) {
                        Text("🏓")
                            .font(.system(size: 32))

                        Button(action: {
                            isPlaying = true
                            currentScore = 0
                            HapticManager.shared.trigger(.selection)
                        }) {
                            Text("Tap to Play")
                                .font(.system(size: 12, weight: .bold))
                                .foregroundColor(.black)
                                .padding(.horizontal, 16)
                                .padding(.vertical, 6)
                                .background(Color.white)
                                .clipShape(Capsule())
                        }
                    }
                } else {
                    VStack {
                        // Bouncing target
                        Text("⚽️")
                            .font(.system(size: 24))
                            .offset(x: paddlePosition * 0.4, y: 0)

                        Spacer()

                        // Draggable Paddle
                        Capsule()
                            .fill(UsColors.postbookPrimary)
                            .frame(width: 60, height: 10)
                            .offset(x: paddlePosition)
                            .gesture(
                                DragGesture()
                                    .onChanged { val in
                                        paddlePosition = min(max(val.translation.width, -80), 80)
                                        currentScore += 1
                                        HapticManager.shared.trigger(.light)
                                    }
                                    .onEnded { _ in
                                        if currentScore > highScore {
                                            HapticManager.shared.trigger(.success)
                                            ToastManager.shared.show("New High Score: \(currentScore) 🏆", style: .success)
                                        }
                                        onScoreSubmitted(currentScore)
                                        isPlaying = false
                                    }
                            )
                    }
                    .padding(14)
                }
            }
            .frame(height: 140)
        }
        .padding(14)
        .background(
            LinearGradient(
                colors: [Color(red: 0x2A/255.0, green: 0x1A/255.0, blue: 0x4A/255.0), Color(red: 0x1A/255.0, green: 0x14/255.0, blue: 0x2A/255.0)],
                startPoint: .topLeading,
                endPoint: .bottomTrailing
            )
        )
        .clipShape(RoundedRectangle(cornerRadius: 18))
        .overlay(RoundedRectangle(cornerRadius: 18).stroke(Color.purple.opacity(0.4), lineWidth: 1.5))
        .frame(width: 290)
    }
}
