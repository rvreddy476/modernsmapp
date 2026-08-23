import SwiftUI
import UsModel
import UsDesignSystem

public struct CreatorMediaKitView: View {
    public let creatorName: String
    public let onDismiss: () -> Void

    public init(
        creatorName: String = "Sarah Chen",
        onDismiss: @escaping () -> Void = {}
    ) {
        self.creatorName = creatorName
        self.onDismiss = onDismiss
    }

    public var body: some View {
        NavigationStack {
            ZStack {
                UsColors.bgPrimary
                    .ignoresSafeArea()

                ScrollView {
                    VStack(alignment: .leading, spacing: 20) {
                        // Verified Media Kit Header
                        VStack(spacing: 8) {
                            UsAvatar(name: creatorName, size: .large)
                                .overlay(Circle().stroke(UsColors.postbookPrimary, lineWidth: 2))

                            Text(creatorName)
                                .font(.system(size: 18, weight: .bold))
                                .foregroundColor(UsColors.textPrimary)

                            HStack(spacing: 6) {
                                Image(systemName: "checkmark.seal.fill")
                                    .foregroundColor(UsColors.postbookPrimary)
                                Text("Verified Creator Media Kit 📊")
                                    .font(.system(size: 12, weight: .semibold))
                                    .foregroundColor(UsColors.postbookPrimary)
                            }
                        }
                        .padding(18)
                        .frame(maxWidth: .infinity)
                        .background(UsColors.bgSecondary)
                        .clipShape(RoundedRectangle(cornerRadius: 18))

                        // Audience & Performance Stats Grid
                        Text("Verified Reach Metrics (Last 30 Days)")
                            .font(.system(size: 15, weight: .bold))
                            .foregroundColor(UsColors.textPrimary)

                        LazyVGrid(columns: [GridItem(.flexible()), GridItem(.flexible())], spacing: 12) {
                            metricBox(title: "Monthly Impressions", value: "2.4M", trend: "+18.2%")
                            metricBox(title: "Avg. Reel Views", value: "142K", trend: "High Retention")
                            metricBox(title: "Engagement Rate", value: "7.8%", trend: "3.2x avg")
                            metricBox(title: "Top Audience", value: "India (82%)", trend: "18-34 yrs")
                        }

                        // Sponsorship Rate Card
                        Text("Standard Commercial Rates")
                            .font(.system(size: 15, weight: .bold))
                            .foregroundColor(UsColors.textPrimary)

                        VStack(spacing: 10) {
                            rateRow(title: "1x Dedicated 60s Reel Integration", price: "₹45,000")
                            rateRow(title: "3x Story Slides with Swipe-up Link", price: "₹18,000")
                            rateRow(title: "Co-Host Live Stream (30 mins)", price: "₹25,000")
                        }
                        .padding(14)
                        .background(UsColors.bgSecondary)
                        .clipShape(RoundedRectangle(cornerRadius: 14))

                        // Export Button
                        Button(action: {
                            HapticManager.shared.trigger(.success)
                            ToastManager.shared.show("Exported Verified Media Kit PDF to Files!", style: .success)
                        }) {
                            HStack {
                                Spacer()
                                Image(systemName: "arrow.down.doc.fill")
                                Text("Download Official PDF Media Kit")
                                    .font(.system(size: 14, weight: .bold))
                                Spacer()
                            }
                            .padding(.vertical, 14)
                            .foregroundColor(.black)
                            .background(Color.white)
                            .clipShape(RoundedRectangle(cornerRadius: 12))
                        }
                    }
                    .padding(16)
                }
            }
            .navigationTitle("Media Kit")
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
    private func metricBox(title: String, value: String, trend: String) -> some View {
        VStack(alignment: .leading, spacing: 4) {
            Text(title)
                .font(.system(size: 11))
                .foregroundColor(UsColors.textMuted)

            Text(value)
                .font(.system(size: 18, weight: .black, design: .rounded))
                .foregroundColor(UsColors.textPrimary)

            Text(trend)
                .font(.system(size: 10, weight: .semibold))
                .foregroundColor(UsColors.onlineGreen)
        }
        .padding(12)
        .frame(maxWidth: .infinity, alignment: .leading)
        .background(UsColors.bgSecondary)
        .clipShape(RoundedRectangle(cornerRadius: 12))
    }

    @ViewBuilder
    private func rateRow(title: String, price: String) -> some View {
        HStack {
            Text(title)
                .font(.system(size: 12, weight: .medium))
                .foregroundColor(UsColors.textPrimary)

            Spacer()

            Text(price)
                .font(.system(size: 13, weight: .bold, design: .monospaced))
                .foregroundColor(UsColors.onlineGreen)
        }
    }
}
