import SwiftUI
import UsModel
import UsDesignSystem

public struct DigitalCollectibleItem: Identifiable {
    public let id: String
    public let title: String
    public let creatorName: String
    public let edition: String
    public let rarity: String
    public let badgeEmoji: String

    public init(id: String, title: String, creatorName: String, edition: String, rarity: String, badgeEmoji: String) {
        self.id = id
        self.title = title
        self.creatorName = creatorName
        self.edition = edition
        self.rarity = rarity
        self.badgeEmoji = badgeEmoji
    }
}

public struct CollectiblesVaultView: View {
    public let onDismiss: () -> Void

    @State private var items: [DigitalCollectibleItem] = [
        DigitalCollectibleItem(id: "col-1", title: "Genesis Pioneer Badge #042", creatorName: "US Founding Team", edition: "#42 of 500", rarity: "Legendary ⭐️", badgeEmoji: "💎"),
        DigitalCollectibleItem(id: "col-2", title: "Bangalore Tech Summit 2026 Pass", creatorName: "Sarah Chen", edition: "#188 of 1,000", rarity: "Rare ⚡️", badgeEmoji: "🚀"),
        DigitalCollectibleItem(id: "col-3", title: "100-Reel Milestone Hologram", creatorName: "Marcus Vance", edition: "#012 of 250", rarity: "Epic 🔥", badgeEmoji: "👑")
    ]

    public init(onDismiss: @escaping () -> Void = {}) {
        self.onDismiss = onDismiss
    }

    public var body: some View {
        NavigationStack {
            ZStack {
                UsColors.bgPrimary
                    .ignoresSafeArea()

                ScrollView {
                    VStack(alignment: .leading, spacing: 20) {
                        // Header
                        HStack(spacing: 12) {
                            ZStack {
                                Circle().fill(Color.purple.opacity(0.2)).frame(width: 44, height: 44)
                                Image(systemName: "sparkles.rectangle.stack.fill")
                                    .foregroundColor(Color.purple)
                                    .font(.system(size: 20))
                            }

                            VStack(alignment: .leading, spacing: 2) {
                                Text("Creator Collectibles & Badges 💎")
                                    .font(.system(size: 15, weight: .bold))
                                    .foregroundColor(UsColors.textPrimary)
                                Text("Limited-edition digital passes & creator rewards")
                                    .font(.system(size: 11))
                                    .foregroundColor(UsColors.textMuted)
                            }
                        }
                        .padding(14)
                        .frame(maxWidth: .infinity, alignment: .leading)
                        .background(UsColors.bgSecondary)
                        .clipShape(RoundedRectangle(cornerRadius: 16))

                        Text("Your Digital Vault (\(items.count))")
                            .font(.system(size: 15, weight: .bold))
                            .foregroundColor(UsColors.textPrimary)

                        LazyVStack(spacing: 14) {
                            ForEach(items) { item in
                                collectibleCard(item)
                            }
                        }
                    }
                    .padding(16)
                }
            }
            .navigationTitle("Collectibles")
            .navigationBarTitleDisplayMode(.inline)
            .toolbar {
                ToolbarItem(placement: .cancellationAction) {
                    Button("Close", action: onDismiss)
                        .foregroundColor(UsColors.textMuted)
                }
            }
        }
    }

    @ViewBuilder
    private func collectibleCard(_ item: DigitalCollectibleItem) -> some View {
        VStack(alignment: .leading, spacing: 12) {
            HStack(spacing: 12) {
                Text(item.badgeEmoji)
                    .font(.system(size: 32))
                    .padding(10)
                    .background(Color.white.opacity(0.1))
                    .clipShape(RoundedRectangle(cornerRadius: 12))

                VStack(alignment: .leading, spacing: 2) {
                    Text(item.title)
                        .font(.system(size: 14, weight: .bold))
                        .foregroundColor(.white)

                    Text("By \(item.creatorName) • \(item.edition)")
                        .font(.system(size: 11))
                        .foregroundColor(.white.opacity(0.7))
                }

                Spacer()

                Text(item.rarity)
                    .font(.system(size: 10, weight: .black))
                    .foregroundColor(Color.yellow)
                    .padding(.horizontal, 8)
                    .padding(.vertical, 4)
                    .background(Color.yellow.opacity(0.2))
                    .clipShape(Capsule())
            }

            Divider().background(Color.white.opacity(0.15))

            HStack {
                Text("Stored on Hardware Keychain")
                    .font(.system(size: 10))
                    .foregroundColor(.white.opacity(0.6))

                Spacer()

                Button(action: {
                    HapticManager.shared.trigger(.selection)
                    ToastManager.shared.show("Showcased badge on Profile!", style: .success)
                }) {
                    Text("Showcase on Profile")
                        .font(.system(size: 11, weight: .bold))
                        .foregroundColor(.black)
                        .padding(.horizontal, 12)
                        .padding(.vertical, 6)
                        .background(Color.white)
                        .clipShape(Capsule())
                }
            }
        }
        .padding(16)
        .background(
            LinearGradient(
                colors: [Color(red: 0x22/255.0, green: 0x18/255.0, blue: 0x38/255.0), Color(red: 0x14/255.0, green: 0x14/255.0, blue: 0x22/255.0)],
                startPoint: .topLeading,
                endPoint: .bottomTrailing
            )
        )
        .clipShape(RoundedRectangle(cornerRadius: 18))
        .overlay(RoundedRectangle(cornerRadius: 18).stroke(UsColors.postbookPrimary.opacity(0.3), lineWidth: 1))
    }
}
