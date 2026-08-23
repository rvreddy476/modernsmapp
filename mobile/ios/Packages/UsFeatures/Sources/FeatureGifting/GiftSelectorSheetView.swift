import SwiftUI
import UsModel
import UsDesignSystem
import UsNetwork

public struct GiftOption: Identifiable, Hashable {
    public let id: String
    public let name: String
    public let emoji: String
    public let coinPrice: Int
    public let color: Color

    public init(id: String, name: String, emoji: String, coinPrice: Int, color: Color = .orange) {
        self.id = id
        self.name = name
        self.emoji = emoji
        self.coinPrice = coinPrice
        self.color = color
    }
}

public struct GiftSelectorSheetView: View {
    public let recipientName: String
    public let onSendGift: (GiftOption) -> Void
    public let onDismiss: () -> Void

    @State private var userCoinBalance: Int = 1250
    @State private var selectedGift: GiftOption? = nil

    private let gifts: [GiftOption] = [
        GiftOption(id: "g1", name: "Kadak Chai", emoji: "☕️", coinPrice: 10, color: .orange),
        GiftOption(id: "g2", name: "Hot Samosa", emoji: "🥟", coinPrice: 25, color: .yellow),
        GiftOption(id: "g3", name: "Diwali Rocket", emoji: "🚀", coinPrice: 100, color: .red),
        GiftOption(id: "g4", name: "Lotus Bloom", emoji: "🪷", coinPrice: 250, color: .pink),
        GiftOption(id: "g5", name: "Gold Crown", emoji: "👑", coinPrice: 500, color: .yellow),
        GiftOption(id: "g6", name: "Super Diamond", emoji: "💎", coinPrice: 1000, color: .cyan)
    ]

    private let columns = [
        GridItem(.flexible(), spacing: 12),
        GridItem(.flexible(), spacing: 12),
        GridItem(.flexible(), spacing: 12)
    ]

    public init(
        recipientName: String = "Sarah",
        onSendGift: @escaping (GiftOption) -> Void = { _ in },
        onDismiss: @escaping () -> Void = {}
    ) {
        self.recipientName = recipientName
        self.onSendGift = onSendGift
        self.onDismiss = onDismiss
    }

    public var body: some View {
        NavigationStack {
            ZStack {
                UsColors.bgPrimary
                    .ignoresSafeArea()

                VStack(spacing: 20) {
                    // Coin balance bar
                    HStack {
                        HStack(spacing: 6) {
                            Text("🪙")
                            Text("\(userCoinBalance) Coins")
                                .font(.system(size: 14, weight: .bold, design: .rounded))
                                .foregroundColor(UsColors.textPrimary)
                        }
                        .padding(.horizontal, 12)
                        .padding(.vertical, 6)
                        .background(UsColors.bgSecondary)
                        .clipShape(Capsule())

                        Spacer()

                        Button("+ Buy Coins") {
                            userCoinBalance += 500
                            ToastManager.shared.show("+500 Coins added via UPI", style: .success)
                        }
                        .font(.system(size: 12, weight: .bold))
                        .foregroundColor(UsColors.postbookPrimary)
                    }

                    // Gifts Grid
                    LazyVGrid(columns: columns, spacing: 12) {
                        ForEach(gifts) { gift in
                            giftCell(gift)
                        }
                    }

                    Spacer()

                    // Send Gift Button
                    Button(action: sendGift) {
                        HStack {
                            Spacer()
                            if let g = selectedGift {
                                Text("Send \(g.emoji) \(g.name) (\(g.coinPrice) Coins)")
                                    .font(.system(size: 15, weight: .bold))
                                    .foregroundColor(.black)
                            } else {
                                Text("Select a Gift for \(recipientName)")
                                    .font(.system(size: 15, weight: .bold))
                                    .foregroundColor(Color.black.opacity(0.5))
                            }
                            Spacer()
                        }
                        .padding(.vertical, 16)
                        .background(selectedGift != nil ? Color.white : Color.white.opacity(0.3))
                        .clipShape(RoundedRectangle(cornerRadius: 14))
                    }
                    .disabled(selectedGift == nil || userCoinBalance < (selectedGift?.coinPrice ?? 0))
                }
                .padding(16)
            }
            .navigationTitle("Send Gift")
            .navigationBarTitleDisplayMode(.inline)
            .toolbar {
                ToolbarItem(placement: .cancellationAction) {
                    Button("Cancel", action: onDismiss)
                        .foregroundColor(UsColors.textMuted)
                }
            }
        }
    }

    @ViewBuilder
    private func giftCell(_ gift: GiftOption) -> some View {
        let isSelected = selectedGift?.id == gift.id
        Button(action: {
            selectedGift = gift
            HapticManager.shared.trigger(.selection)
        }) {
            VStack(spacing: 6) {
                Text(gift.emoji)
                    .font(.system(size: 36))

                Text(gift.name)
                    .font(.system(size: 12, weight: .semibold))
                    .foregroundColor(UsColors.textPrimary)
                    .lineLimit(1)

                HStack(spacing: 2) {
                    Text("🪙")
                        .font(.system(size: 10))
                    Text("\(gift.coinPrice)")
                        .font(.system(size: 12, weight: .bold, design: .rounded))
                        .foregroundColor(UsColors.textMuted)
                }
            }
            .frame(maxWidth: .infinity)
            .padding(.vertical, 14)
            .background(isSelected ? UsColors.postbookPrimary.opacity(0.15) : UsColors.bgSecondary)
            .clipShape(RoundedRectangle(cornerRadius: 14))
            .overlay(
                RoundedRectangle(cornerRadius: 14)
                    .stroke(isSelected ? UsColors.postbookPrimary : UsColors.borderSubtle, lineWidth: isSelected ? 2 : 1)
            )
        }
        .buttonStyle(.plain)
    }

    private func sendGift() {
        guard let gift = selectedGift, userCoinBalance >= gift.coinPrice else { return }
        userCoinBalance -= gift.coinPrice
        HapticManager.shared.trigger(.success)
        onSendGift(gift)
        ToastManager.shared.show("🎉 Sent \(gift.emoji) \(gift.name) to \(recipientName)!", style: .success)
        onDismiss()
    }
}
