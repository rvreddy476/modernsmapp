import SwiftUI
import UsModel
import UsDesignSystem
import UsNetwork

public struct CreatorStudioView: View {
    public let onDismiss: () -> Void

    @State private var totalEarningsPaise: Int64 = 8450000 // ₹84,500
    @State private var monthlyViews: Int = 1420000 // 1.42M
    @State private var totalSubscribers: Int = 48500
    @State private var isWithdrawing: Bool = false

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
                        // Earnings Dashboard Card
                        earningsCard

                        // Performance Summary Grid
                        performanceGrid

                        // Monetization Breakdown
                        monetizationBreakdown

                        // Payout Button
                        withdrawButton
                    }
                    .padding(16)
                }
            }
            .navigationTitle("Creator Studio")
            .navigationBarTitleDisplayMode(.inline)
            .toolbar {
                ToolbarItem(placement: .cancellationAction) {
                    Button("Close", action: onDismiss)
                        .foregroundColor(UsColors.textMuted)
                }
            }
        }
    }

    private var earningsCard: some View {
        VStack(alignment: .leading, spacing: 14) {
            Text("Estimated Revenue (Last 28 Days)")
                .font(.system(size: 13, weight: .medium))
                .foregroundColor(.white.opacity(0.8))

            Text("₹84,500.00")
                .font(.system(size: 34, weight: .black, design: .rounded))
                .foregroundColor(.white)

            HStack(spacing: 6) {
                Image(systemName: "arrow.up.right")
                    .foregroundColor(UsColors.onlineGreen)
                Text("+24.5% vs previous period")
                    .font(.system(size: 13, weight: .semibold))
                    .foregroundColor(UsColors.onlineGreen)
            }
        }
        .padding(20)
        .frame(maxWidth: .infinity, alignment: .leading)
        .background(
            LinearGradient(
                colors: [Color(red: 0x2A/255.0, green: 0x1A/255.0, blue: 0x4E/255.0),
                         Color(red: 0x14/255.0, green: 0x14/255.0, blue: 0x28/255.0)],
                startPoint: .topLeading,
                endPoint: .bottomTrailing
            )
        )
        .clipShape(RoundedRectangle(cornerRadius: 18))
    }

    private var performanceGrid: some View {
        HStack(spacing: 12) {
            statCard(title: "Views", value: "1.4M", subtitle: "+180K this week")
            statCard(title: "Subscribers", value: "48.5K", subtitle: "+2.1K new")
            statCard(title: "Watch Time", value: "32.4K hrs", subtitle: "Avg 4:12m")
        }
    }

    private func statCard(title: String, value: String, subtitle: String) -> some View {
        VStack(alignment: .leading, spacing: 6) {
            Text(title)
                .font(.system(size: 12))
                .foregroundColor(UsColors.textMuted)
            Text(value)
                .font(.system(size: 18, weight: .bold, design: .rounded))
                .foregroundColor(UsColors.textPrimary)
            Text(subtitle)
                .font(.system(size: 10))
                .foregroundColor(UsColors.onlineGreen)
        }
        .padding(14)
        .frame(maxWidth: .infinity, alignment: .leading)
        .background(UsColors.bgSecondary)
        .clipShape(RoundedRectangle(cornerRadius: 14))
    }

    private var monetizationBreakdown: some View {
        VStack(alignment: .leading, spacing: 12) {
            Text("Revenue Streams")
                .font(.system(size: 16, weight: .bold))
                .foregroundColor(UsColors.textPrimary)

            VStack(spacing: 10) {
                revenueRow(title: "Video Ads & PostTube", amount: "₹48,200", icon: "play.rectangle.fill", color: UsColors.posttubePrimary)
                revenueRow(title: "Viewer Tips & SuperChats", amount: "₹21,800", icon: "heart.circle.fill", color: UsColors.postgramPrimary)
                revenueRow(title: "Channel Memberships", amount: "₹14,500", icon: "star.circle.fill", color: UsColors.postbookPrimary)
            }
        }
    }

    private func revenueRow(title: String, amount: String, icon: String, color: Color) -> some View {
        HStack(spacing: 12) {
            ZStack {
                Circle().fill(color.opacity(0.15)).frame(width: 40, height: 40)
                Image(systemName: icon).foregroundColor(color).font(.system(size: 18))
            }
            Text(title).font(.system(size: 14, weight: .medium)).foregroundColor(UsColors.textPrimary)
            Spacer()
            Text(amount).font(.system(size: 15, weight: .bold, design: .rounded)).foregroundColor(UsColors.textPrimary)
        }
        .padding(12)
        .background(UsColors.bgSecondary)
        .clipShape(RoundedRectangle(cornerRadius: 12))
    }

    private var withdrawButton: some View {
        Button(action: {
            isWithdrawing = true
            DispatchQueue.main.asyncAfter(deadline: .now() + 1.2) {
                isWithdrawing = false
                ToastManager.shared.show("₹84,500 Transferred to Primary Bank Account", style: .success)
            }
        }) {
            HStack {
                Spacer()
                if isWithdrawing {
                    ProgressView().tint(.black)
                } else {
                    Text("Transfer to Bank (UPI Instant)")
                        .font(.system(size: 15, weight: .bold))
                        .foregroundColor(.black)
                }
                Spacer()
            }
            .padding(.vertical, 16)
            .background(Color.white)
            .clipShape(RoundedRectangle(cornerRadius: 14))
        }
        .disabled(isWithdrawing)
        .padding(.top, 8)
    }
}
