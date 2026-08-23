import SwiftUI
import UsModel
import UsDesignSystem
import UsNetwork

public struct SubscriptionTier: Identifiable, Hashable {
    public let id: String
    public let name: String
    public let priceMonthly: String
    public let description: String
    public let perks: [String]
    public let badgeColor: Color
    public let isPopular: Bool

    public init(
        id: String,
        name: String,
        priceMonthly: String,
        description: String,
        perks: [String],
        badgeColor: Color = Color.blue,
        isPopular: Bool = false
    ) {
        self.id = id
        self.name = name
        self.priceMonthly = priceMonthly
        self.description = description
        self.perks = perks
        self.badgeColor = badgeColor
        self.isPopular = isPopular
    }
}

public struct CreatorSubscriptionView: View {
    public let creator: Author
    public let onDismiss: () -> Void

    @State private var selectedTierId: String = "tier-2"
    @State private var isSubscribing: Bool = false

    private let tiers: [SubscriptionTier] = [
        SubscriptionTier(
            id: "tier-1",
            name: "Supporter",
            priceMonthly: "₹49/mo",
            description: "Show your support and get a subscriber loyalty badge next to your comments.",
            perks: ["Exclusive loyalty star badge ⭐", "Access to subscriber-only stories", "Member-only chat stickers"],
            badgeColor: Color.blue
        ),
        SubscriptionTier(
            id: "tier-2",
            name: "Insider Pass",
            priceMonthly: "₹199/mo",
            description: "Full access to behind-the-scenes posts, uncut tutorials, and early access to drops.",
            perks: ["All Supporter perks", "Behind-the-scenes Reels & Posts 🎬", "Monthly live Q&A sessions", "Early access to merchandise drops"],
            badgeColor: Color.purple,
            isPopular: true
        ),
        SubscriptionTier(
            id: "tier-3",
            name: "VIP Inner Circle",
            priceMonthly: "₹499/mo",
            description: "Direct priority DMs with the creator and 1-on-1 monthly live hangout.",
            perks: ["All Insider perks", "Priority DM replies in inbox 💬", "1-on-1 monthly group video call", "Special shoutout in video credits"],
            badgeColor: Color.orange
        )
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

                ScrollView {
                    VStack(spacing: 20) {
                        // Header
                        VStack(spacing: 10) {
                            UsAvatar(name: creator.nameForDisplay, url: creator.avatarUrl, size: .large)
                                .frame(width: 80, height: 80)

                            Text("Subscribe to \(creator.nameForDisplay)")
                                .font(.system(size: 22, weight: .bold))
                                .foregroundColor(UsColors.textPrimary)

                            Text("Unlock exclusive content, direct access, and support creator work directly.")
                                .font(.system(size: 13))
                                .foregroundColor(UsColors.textMuted)
                                .multilineTextAlignment(.center)
                                .padding(.horizontal, 24)
                        }
                        .padding(.top, 12)

                        // Tiers List
                        VStack(spacing: 16) {
                            ForEach(tiers) { tier in
                                tierCard(tier)
                            }
                        }

                        // Subscribe button
                        Button(action: subscribe) {
                            HStack {
                                Spacer()
                                if isSubscribing {
                                    ProgressView().tint(.black)
                                } else {
                                    Text("Subscribe with US Wallet / UPI")
                                        .font(.system(size: 15, weight: .bold))
                                        .foregroundColor(.black)
                                }
                                Spacer()
                            }
                            .padding(.vertical, 16)
                            .background(Color.white)
                            .clipShape(RoundedRectangle(cornerRadius: 14))
                        }
                        .disabled(isSubscribing)
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

    @ViewBuilder
    private func tierCard(_ tier: SubscriptionTier) -> some View {
        let isSelected = selectedTierId == tier.id
        Button(action: {
            selectedTierId = tier.id
            HapticManager.shared.trigger(.selection)
        }) {
            VStack(alignment: .leading, spacing: 12) {
                HStack {
                    VStack(alignment: .leading, spacing: 2) {
                        Text(tier.name)
                            .font(.system(size: 17, weight: .bold))
                            .foregroundColor(UsColors.textPrimary)
                        Text(tier.priceMonthly)
                            .font(.system(size: 15, weight: .bold, design: .rounded))
                            .foregroundColor(tier.badgeColor)
                    }

                    Spacer()

                    if tier.isPopular {
                        Text("MOST POPULAR")
                            .font(.system(size: 10, weight: .black))
                            .foregroundColor(.black)
                            .padding(.horizontal, 8)
                            .padding(.vertical, 4)
                            .background(Color.yellow)
                            .clipShape(Capsule())
                    }

                    Image(systemName: isSelected ? "checkmark.circle.fill" : "circle")
                        .font(.system(size: 20))
                        .foregroundColor(isSelected ? UsColors.postbookPrimary : UsColors.textDim)
                }

                Text(tier.description)
                    .font(.system(size: 12))
                    .foregroundColor(UsColors.textMuted)

                Divider().background(UsColors.borderSubtle)

                VStack(alignment: .leading, spacing: 6) {
                    ForEach(tier.perks, id: \.self) { perk in
                        HStack(spacing: 6) {
                            Image(systemName: "checkmark")
                                .font(.system(size: 11, weight: .bold))
                                .foregroundColor(UsColors.onlineGreen)
                            Text(perk)
                                .font(.system(size: 12))
                                .foregroundColor(UsColors.textSecondary)
                        }
                    }
                }
            }
            .padding(16)
            .background(isSelected ? UsColors.bgSecondary : UsColors.bgSecondary.opacity(0.6))
            .clipShape(RoundedRectangle(cornerRadius: 16))
            .overlay(
                RoundedRectangle(cornerRadius: 16)
                    .stroke(isSelected ? UsColors.postbookPrimary : UsColors.borderSubtle, lineWidth: isSelected ? 2 : 1)
            )
        }
        .buttonStyle(.plain)
    }

    private func subscribe() {
        isSubscribing = true
        DispatchQueue.main.asyncAfter(deadline: .now() + 1.2) {
            isSubscribing = false
            HapticManager.shared.trigger(.success)
            ToastManager.shared.show("🎉 Subscribed to \(creator.nameForDisplay)!", style: .success)
            onDismiss()
        }
    }
}
