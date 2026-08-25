import SwiftUI
import UsModel
import UsDesignSystem

public struct SuperAppWidgetsView: View {
    public let onDismiss: () -> Void

    public init(onDismiss: @escaping () -> Void = {}) {
        self.onDismiss = onDismiss
    }

    public var body: some View {
        NavigationStack {
            ZStack {
                UsColors.bgPrimary
                    .ignoresSafeArea()

                ScrollView {
                    VStack(alignment: .leading, spacing: 24) {
                        Text("Interactive Home Screen & Lock Screen Widgets")
                            .font(.system(size: 13))
                            .foregroundColor(UsColors.textMuted)

                        // 1. Digital Gold Live Price Widget (Small)
                        VStack(alignment: .leading, spacing: 8) {
                            Text("1. Digital Gold Live Ticker (Small Widget)")
                                .font(.system(size: 13, weight: .bold))
                                .foregroundColor(UsColors.textPrimary)

                            ZStack(alignment: .bottomLeading) {
                                LinearGradient(
                                    colors: [Color(red: 0x2A/255.0, green: 0x24/255.0, blue: 0x14/255.0), Color(red: 0x14/255.0, green: 0x14/255.0, blue: 0x18/255.0)],
                                    startPoint: .topLeading,
                                    endPoint: .bottomTrailing
                                )

                                VStack(alignment: .leading, spacing: 4) {
                                    HStack {
                                        Text("24K GOLD")
                                            .font(.system(size: 11, weight: .black))
                                            .foregroundColor(Color.yellow)

                                        Spacer()

                                        Image(systemName: "sparkles")
                                            .foregroundColor(Color.yellow)
                                            .font(.system(size: 12))
                                    }

                                    Spacer()

                                    Text("₹7,142.50")
                                        .font(.system(size: 18, weight: .black, design: .rounded))
                                        .foregroundColor(.white)

                                    Text("+1.4% Today • 99.99% Pure")
                                        .font(.system(size: 9, weight: .semibold))
                                        .foregroundColor(UsColors.onlineGreen)
                                }
                                .padding(14)
                            }
                            .frame(width: 160, height: 160)
                            .clipShape(RoundedRectangle(cornerRadius: 24))
                            .overlay(RoundedRectangle(cornerRadius: 24).stroke(Color.yellow.opacity(0.3), lineWidth: 1))
                            .shadow(color: Color.black.opacity(0.3), radius: 8, x: 0, y: 4)
                        }

                        // 2. Daily Rewards & Streak Counter (Medium Widget)
                        VStack(alignment: .leading, spacing: 8) {
                            Text("2. Daily Streak & Super-Cashback (Medium Widget)")
                                .font(.system(size: 13, weight: .bold))
                                .foregroundColor(UsColors.textPrimary)

                            HStack(spacing: 16) {
                                VStack(alignment: .leading, spacing: 4) {
                                    HStack(spacing: 4) {
                                        Text("🔥 18 DAY STREAK")
                                            .font(.system(size: 11, weight: .black))
                                            .foregroundColor(Color.orange)
                                    }

                                    Text("Daily Activity Active")
                                        .font(.system(size: 14, weight: .bold))
                                        .foregroundColor(.white)

                                    Text("2 more days to unlock ₹250 scratch card!")
                                        .font(.system(size: 10))
                                        .foregroundColor(.white.opacity(0.7))
                                }

                                Spacer()

                                VStack(spacing: 6) {
                                    Image(systemName: "gift.fill")
                                        .font(.system(size: 26))
                                        .foregroundColor(Color.yellow)

                                    Text("Claim ₹50")
                                        .font(.system(size: 11, weight: .bold))
                                        .foregroundColor(.black)
                                        .padding(.horizontal, 10)
                                        .padding(.vertical, 4)
                                        .background(Color.white)
                                        .clipShape(Capsule())
                                }
                            }
                            .padding(16)
                            .background(Color(red: 0x1E/255.0, green: 0x18/255.0, blue: 0x2A/255.0))
                            .clipShape(RoundedRectangle(cornerRadius: 24))
                            .overlay(RoundedRectangle(cornerRadius: 24).stroke(UsColors.postbookPrimary.opacity(0.3), lineWidth: 1))
                        }
                    }
                    .padding(16)
                }
            }
            .navigationTitle("App Widgets")
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
