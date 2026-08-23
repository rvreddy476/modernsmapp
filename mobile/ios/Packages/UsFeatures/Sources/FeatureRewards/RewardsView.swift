import SwiftUI
import UsModel
import UsDesignSystem
import UsNetwork

public struct RewardsView: View {
    public let onDismiss: () -> Void

    @State private var streakDays: Int = 5
    @State private var isCardScratched: Bool = false
    @State private var scratchCashbackAmount: String = "₹25"

    public init(onDismiss: @escaping () -> Void = {}) {
        self.onDismiss = onDismiss
    }

    public var body: some View {
        NavigationStack {
            ZStack {
                UsColors.bgPrimary
                    .ignoresSafeArea()

                ScrollView {
                    VStack(spacing: 20) {
                        // Daily Streak Banner
                        streakCard

                        // Interactive Scratch Card
                        scratchCardSection

                        // Milestones & Badges
                        milestonesSection
                    }
                    .padding(16)
                }
            }
            .navigationTitle("Rewards & Streaks")
            .navigationBarTitleDisplayMode(.inline)
            .toolbar {
                ToolbarItem(placement: .cancellationAction) {
                    Button("Close", action: onDismiss)
                        .foregroundColor(UsColors.textMuted)
                }
            }
        }
    }

    private var streakCard: some View {
        HStack(spacing: 16) {
            Text("🔥")
                .font(.system(size: 44))

            VStack(alignment: .leading, spacing: 4) {
                Text("\(streakDays) Day Streak!")
                    .font(.system(size: 20, weight: .bold))
                    .foregroundColor(.white)
                Text("Post daily or send UPI to keep your streak alive.")
                    .font(.system(size: 12))
                    .foregroundColor(.white.opacity(0.85))
            }

            Spacer()
        }
        .padding(18)
        .background(
            LinearGradient(
                colors: [Color.orange, Color.red],
                startPoint: .topLeading,
                endPoint: .bottomTrailing
            )
        )
        .clipShape(RoundedRectangle(cornerRadius: 18))
    }

    private var scratchCardSection: some View {
        VStack(alignment: .leading, spacing: 12) {
            Text("Cashback Scratch Card")
                .font(.system(size: 16, weight: .bold))
                .foregroundColor(UsColors.textPrimary)

            Button(action: {
                if !isCardScratched {
                    isCardScratched = true
                    HapticManager.shared.trigger(.success)
                    ToastManager.shared.show("🎉 \(scratchCashbackAmount) Added to US Wallet!", style: .success)
                }
            }) {
                ZStack {
                    if isCardScratched {
                        VStack(spacing: 6) {
                            Text("You Won!")
                                .font(.system(size: 14, weight: .semibold))
                                .foregroundColor(UsColors.textMuted)
                            Text(scratchCashbackAmount)
                                .font(.system(size: 36, weight: .black, design: .rounded))
                                .foregroundColor(UsColors.onlineGreen)
                            Text("Credited directly to UPI Wallet")
                                .font(.system(size: 11))
                                .foregroundColor(UsColors.textMuted)
                        }
                    } else {
                        VStack(spacing: 8) {
                            Image(systemName: "gift.fill")
                                .font(.system(size: 32))
                                .foregroundColor(.white)
                            Text("Tap to Scratch & Reveal")
                                .font(.system(size: 14, weight: .bold))
                                .foregroundColor(.white)
                        }
                    }
                }
                .frame(maxWidth: .infinity)
                .frame(height: 140)
                .background(
                    isCardScratched ?
                    LinearGradient(colors: [UsColors.bgSecondary, UsColors.bgTertiary], startPoint: .top, endPoint: .bottom) :
                    LinearGradient(colors: [UsColors.postbookPrimary, UsColors.postgramPrimary], startPoint: .topLeading, endPoint: .bottomTrailing)
                )
                .clipShape(RoundedRectangle(cornerRadius: 16))
                .overlay(RoundedRectangle(cornerRadius: 16).stroke(UsColors.borderMedium, lineWidth: 1))
            }
            .buttonStyle(.plain)
        }
    }

    private var milestonesSection: some View {
        VStack(alignment: .leading, spacing: 12) {
            Text("Your Creator Badges")
                .font(.system(size: 16, weight: .bold))
                .foregroundColor(UsColors.textPrimary)

            LazyVGrid(columns: [GridItem(.flexible()), GridItem(.flexible())], spacing: 12) {
                badgeItem(icon: "star.fill", title: "Top Creator", level: "Unlocked", color: Color.yellow)
                badgeItem(icon: "bolt.fill", title: "UPI Super-Payer", level: "Unlocked", color: UsColors.postbookPrimary)
                badgeItem(icon: "bubble.left.and.bubble.right.fill", title: "Top Commenter", level: "Tier 2", color: UsColors.postgramPrimary)
                badgeItem(icon: "sparkles", title: "Trendsetter", level: "Locked", color: UsColors.textDim)
            }
        }
    }

    private func badgeItem(icon: String, title: String, level: String, color: Color) -> some View {
        HStack(spacing: 12) {
            ZStack {
                Circle().fill(color.opacity(0.15)).frame(width: 42, height: 42)
                Image(systemName: icon).foregroundColor(color).font(.system(size: 18))
            }

            VStack(alignment: .leading, spacing: 2) {
                Text(title)
                    .font(.system(size: 13, weight: .semibold))
                    .foregroundColor(UsColors.textPrimary)
                Text(level)
                    .font(.system(size: 11))
                    .foregroundColor(color)
            }
            Spacer()
        }
        .padding(12)
        .background(UsColors.bgSecondary)
        .clipShape(RoundedRectangle(cornerRadius: 12))
    }
}
