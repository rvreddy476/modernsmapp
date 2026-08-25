import SwiftUI
import UsModel
import UsDesignSystem

public struct IcebreakerGameView: View {
    public let groupName: String
    public let onDismiss: () -> Void

    @State private var cards: [String] = [
        "🔥 TRUTH: What's the most useless gadget you've ever bought on a midnight impulse?",
        "⚡️ DARE: Send the 7th photo from your camera roll into this group right now without context!",
        "💡 ICEBREAKER: If you could teleport anywhere in India for lunch today, where are we going?",
        "🎭 TRUTH: Which teammate's commit message has made you laugh the hardest?"
    ]
    @State private var currentCardIndex: Int = 0

    public init(
        groupName: String = "Bangalore Builders Group",
        onDismiss: @escaping () -> Void = {}
    ) {
        self.groupName = groupName
        self.onDismiss = onDismiss
    }

    public var body: some View {
        NavigationStack {
            ZStack {
                UsColors.bgPrimary
                    .ignoresSafeArea()

                VStack(spacing: 24) {
                    Text("Multiplayer Group Icebreakers 🎲")
                        .font(.system(size: 13))
                        .foregroundColor(UsColors.textMuted)

                    // Active Game Card
                    ZStack {
                        RoundedRectangle(cornerRadius: 24)
                            .fill(
                                LinearGradient(
                                    colors: [Color(red: 0x1E/255.0, green: 0x1E/255.0, blue: 0x38/255.0), Color(red: 0x2E/255.0, green: 0x1E/255.0, blue: 0x48/255.0)],
                                    startPoint: .topLeading,
                                    endPoint: .bottomTrailing
                                )
                            )
                            .overlay(RoundedRectangle(cornerRadius: 24).stroke(UsColors.postbookPrimary.opacity(0.4), lineWidth: 1.5))
                            .shadow(color: Color.purple.opacity(0.3), radius: 16, x: 0, y: 8)

                        VStack(spacing: 16) {
                            Text("CARD #\(currentCardIndex + 1) OF \(cards.count)")
                                .font(.system(size: 11, weight: .black))
                                .foregroundColor(UsColors.postbookPrimary)

                            Text(cards[currentCardIndex])
                                .font(.system(size: 18, weight: .bold))
                                .foregroundColor(.white)
                                .multilineTextAlignment(.center)
                                .padding(.horizontal, 20)
                        }
                        .padding(24)
                    }
                    .frame(height: 240)
                    .padding(.horizontal, 20)

                    // Action buttons
                    HStack(spacing: 14) {
                        Button(action: nextCard) {
                            HStack {
                                Image(systemName: "shuffle")
                                Text("Next Card")
                            }
                            .font(.system(size: 14, weight: .bold))
                            .foregroundColor(.black)
                            .padding(.horizontal, 24)
                            .padding(.vertical, 14)
                            .background(Color.white)
                            .clipShape(Capsule())
                        }

                        Button(action: shareToChat) {
                            HStack {
                                Image(systemName: "paperplane.fill")
                                Text("Send to Group")
                            }
                            .font(.system(size: 14, weight: .bold))
                            .foregroundColor(.white)
                            .padding(.horizontal, 20)
                            .padding(.vertical, 14)
                            .background(UsColors.postbookPrimary)
                            .clipShape(Capsule())
                        }
                    }

                    Spacer()
                }
                .padding(.top, 24)
            }
            .navigationTitle("Truth or Dare")
            .navigationBarTitleDisplayMode(.inline)
            .toolbar {
                ToolbarItem(placement: .cancellationAction) {
                    Button("Close", action: onDismiss)
                        .foregroundColor(UsColors.textMuted)
                }
            }
        }
    }

    private func nextCard() {
        HapticManager.shared.trigger(.selection)
        withAnimation(.spring()) {
            currentCardIndex = (currentCardIndex + 1) % cards.count
        }
    }

    private func shareToChat() {
        HapticManager.shared.trigger(.success)
        ToastManager.shared.show("🎲 Card sent to \(groupName)!", style: .success)
        onDismiss()
    }
}
