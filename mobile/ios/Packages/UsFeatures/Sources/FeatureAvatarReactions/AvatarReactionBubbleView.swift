import SwiftUI
import UsModel
import UsDesignSystem

public struct AvatarReactionBubbleView: View {
    public let emoji: String
    public let authorName: String
    public let onBurst: () -> Void

    @State private var scale: CGFloat = 1.0
    @State private var isBursting: Bool = false

    public init(
        emoji: String = "🥳",
        authorName: String = "Sarah Chen",
        onBurst: @escaping () -> Void = {}
    ) {
        self.emoji = emoji
        self.authorName = authorName
        self.onBurst = onBurst
    }

    public var body: some View {
        Button(action: {
            withAnimation(.spring(response: 0.3, dampingFraction: 0.5)) {
                scale = 1.4
            }
            HapticManager.shared.trigger(.success)
            isBursting = true
            onBurst()

            DispatchQueue.main.asyncAfter(deadline: .now() + 0.25) {
                withAnimation(.spring()) {
                    scale = 1.0
                    isBursting = false
                }
            }
        }) {
            HStack(spacing: 8) {
                Text(emoji)
                    .font(.system(size: 28))
                    .scaleEffect(scale)

                VStack(alignment: .leading, spacing: 2) {
                    Text(authorName)
                        .font(.system(size: 13, weight: .bold))
                        .foregroundColor(.white)
                    Text("Sent a reaction bubble 💥")
                        .font(.system(size: 10))
                        .foregroundColor(.white.opacity(0.7))
                }
            }
            .padding(.horizontal, 14)
            .padding(.vertical, 10)
            .background(Color.black.opacity(0.8))
            .clipShape(Capsule())
            .overlay(Capsule().stroke(Color.white.opacity(0.3), lineWidth: 1))
            .shadow(color: Color.purple.opacity(0.4), radius: 8, x: 0, y: 4)
        }
        .buttonStyle(.plain)
    }
}
