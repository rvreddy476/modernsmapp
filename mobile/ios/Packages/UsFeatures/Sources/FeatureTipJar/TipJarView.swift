import SwiftUI
import UsModel
import UsDesignSystem
import UsNetwork

public struct TipChip: Identifiable, Hashable {
    public let id: String
    public let amount: Int
    public let title: String
    public let emoji: String

    public init(id: String, amount: Int, title: String, emoji: String) {
        self.id = id
        self.amount = amount
        self.title = title
        self.emoji = emoji
    }
}

public struct TipJarView: View {
    public let creator: Author
    public let onDismiss: () -> Void

    @State private var selectedAmount: Int = 50
    @State private var customMessage: String = "Keep up the awesome content! 🚀"
    @State private var isSendingTip: Bool = false

    private let chips: [TipChip] = [
        TipChip(id: "tc-1", amount: 20, title: "Kadak Chai", emoji: "☕️"),
        TipChip(id: "tc-2", amount: 50, title: "Sweet Treat", emoji: "🍰"),
        TipChip(id: "tc-3", amount: 100, title: "Pure Legend", emoji: "🏆"),
        TipChip(id: "tc-4", amount: 500, title: "Superstar", emoji: "🌟")
    ]

    public init(
        creator: Author = Author(id: "c1", username: "sarah_c", displayName: "Sarah Chen"),
        onDismiss: @escaping () -> Void = {}
    ) {
        self.creator = creator
        self.onDismiss = onDismiss
    }

    public var body: some View {
        NavigationStack {
            ZStack {
                UsColors.bgPrimary
                    .ignoresSafeArea()

                VStack(spacing: 20) {
                    // Creator Info
                    VStack(spacing: 8) {
                        UsAvatar(name: creator.nameForDisplay, url: creator.avatarUrl, size: .large)
                        Text("Send a Tip to \(creator.nameForDisplay)")
                            .font(.system(size: 18, weight: .bold))
                            .foregroundColor(UsColors.textPrimary)
                        Text("100% of your tip goes directly to the creator's UPI wallet.")
                            .font(.system(size: 12))
                            .foregroundColor(UsColors.textMuted)
                    }
                    .padding(.top, 12)

                    // Tip Amount Chips
                    HStack(spacing: 10) {
                        ForEach(chips) { chip in
                            let isSelected = selectedAmount == chip.amount
                            Button(action: {
                                selectedAmount = chip.amount
                                HapticManager.shared.trigger(.selection)
                            }) {
                                VStack(spacing: 4) {
                                    Text(chip.emoji)
                                        .font(.system(size: 24))
                                    Text("₹\(chip.amount)")
                                        .font(.system(size: 14, weight: .bold, design: .rounded))
                                        .foregroundColor(isSelected ? .black : UsColors.textPrimary)
                                    Text(chip.title)
                                        .font(.system(size: 10))
                                        .foregroundColor(isSelected ? Color.black.opacity(0.8) : UsColors.textMuted)
                                }
                                .frame(maxWidth: .infinity)
                                .padding(.vertical, 12)
                                .background(isSelected ? Color.white : UsColors.bgSecondary)
                                .clipShape(RoundedRectangle(cornerRadius: 14))
                                .overlay(
                                    RoundedRectangle(cornerRadius: 14)
                                        .stroke(isSelected ? Color.white : UsColors.borderSubtle, lineWidth: 1)
                                )
                            }
                            .buttonStyle(.plain)
                        }
                    }

                    // Optional message
                    VStack(alignment: .leading, spacing: 6) {
                        Text("Add a personalized note")
                            .font(.system(size: 12, weight: .semibold))
                            .foregroundColor(UsColors.textMuted)

                        TextField("Type your message...", text: $customMessage)
                            .textFieldStyle(.plain)
                            .padding(14)
                            .background(UsColors.bgSecondary)
                            .clipShape(RoundedRectangle(cornerRadius: 12))
                            .foregroundColor(UsColors.textPrimary)
                    }

                    Spacer()

                    // Send Tip Button
                    Button(action: sendTip) {
                        HStack {
                            Spacer()
                            if isSendingTip {
                                ProgressView().tint(.black)
                            } else {
                                Text("Tip ₹\(selectedAmount) via US Wallet / UPI")
                                    .font(.system(size: 15, weight: .bold))
                                    .foregroundColor(.black)
                            }
                            Spacer()
                        }
                        .padding(.vertical, 16)
                        .background(Color.white)
                        .clipShape(RoundedRectangle(cornerRadius: 14))
                    }
                    .disabled(isSendingTip)
                }
                .padding(16)
            }
            .navigationTitle("Tip Creator")
            .navigationBarTitleDisplayMode(.inline)
            .toolbar {
                ToolbarItem(placement: .cancellationAction) {
                    Button("Close", action: onDismiss)
                        .foregroundColor(UsColors.textMuted)
                }
            }
        }
    }

    private func sendTip() {
        isSendingTip = true
        DispatchQueue.main.asyncAfter(deadline: .now() + 1.2) {
            isSendingTip = false
            HapticManager.shared.trigger(.success)
            ToastManager.shared.show("🎉 ₹\(selectedAmount) Tip Sent to \(creator.nameForDisplay)!", style: .success)
            onDismiss()
        }
    }
}
