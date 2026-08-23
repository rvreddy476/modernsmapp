import SwiftUI
import UsModel
import UsDesignSystem

public struct SlambookEntry: Identifiable {
    public let id = UUID()
    public let question: String
    public var answer: String
}

public struct SlambookView: View {
    public let friendName: String
    public let onDismiss: () -> Void

    @State private var entries: [SlambookEntry] = [
        SlambookEntry(question: "Nickname you gave me:", answer: "Captain Code 🚀"),
        SlambookEntry(question: "First impression of me:", answer: "Quiet genius who drinks way too much coffee ☕️"),
        SlambookEntry(question: "Song that reminds you of me:", answer: "Midnight City - M83 🎶"),
        SlambookEntry(question: "One thing you admire about me:", answer: "Unstoppable persistence when building new things!"),
        SlambookEntry(question: "Best memory together:", answer: "Late night hackathons and chai tapri debates in Indiranagar.")
    ]

    public init(friendName: String = "Alex", onDismiss: @escaping () -> Void = {}) {
        self.friendName = friendName
        self.onDismiss = onDismiss
    }

    public var body: some View {
        NavigationStack {
            ZStack {
                UsColors.bgPrimary
                    .ignoresSafeArea()

                ScrollView {
                    VStack(spacing: 20) {
                        // Title Header
                        VStack(spacing: 6) {
                            Text("📖 \(friendName)'s Slambook")
                                .font(.system(size: 24, weight: .bold))
                                .foregroundColor(UsColors.textPrimary)

                            Text("Answer fun prompts to create a permanent friend memory card.")
                                .font(.system(size: 13))
                                .foregroundColor(UsColors.textMuted)
                                .multilineTextAlignment(.center)
                        }
                        .padding(.top, 12)

                        // Prompt Cards
                        VStack(spacing: 14) {
                            ForEach($entries) { $entry in
                                VStack(alignment: .leading, spacing: 8) {
                                    Text(entry.question)
                                        .font(.system(size: 13, weight: .bold))
                                        .foregroundColor(UsColors.postgramPrimary)

                                    TextField("Type answer...", text: $entry.answer)
                                        .font(.system(size: 14))
                                        .foregroundColor(UsColors.textPrimary)
                                        .padding(12)
                                        .background(UsColors.bgTertiary)
                                        .clipShape(RoundedRectangle(cornerRadius: 10))
                                }
                                .padding(14)
                                .background(UsColors.bgSecondary)
                                .clipShape(RoundedRectangle(cornerRadius: 14))
                            }
                        }

                        // Submit / Share Button
                        Button(action: {
                            HapticManager.shared.trigger(.success)
                            ToastManager.shared.show("Slambook Entry Saved & Sent to \(friendName)!", style: .success)
                            onDismiss()
                        }) {
                            HStack {
                                Spacer()
                                Text("Save to Slambook & Share")
                                    .font(.system(size: 15, weight: .bold))
                                    .foregroundColor(.black)
                                Spacer()
                            }
                            .padding(.vertical, 16)
                            .background(Color.white)
                            .clipShape(RoundedRectangle(cornerRadius: 14))
                        }
                        .padding(.top, 8)
                    }
                    .padding(16)
                }
            }
            .navigationBarTitleDisplayMode(.inline)
            .toolbar {
                ToolbarItem(placement: .cancellationAction) {
                    Button("Close", action: onDismiss)
                        .foregroundColor(UsColors.textMuted)
                }
            }
        }
    }
}
