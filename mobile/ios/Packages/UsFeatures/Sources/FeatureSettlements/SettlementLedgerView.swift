import SwiftUI
import UsModel
import UsDesignSystem
import UsNetwork

public struct PayoutSettlementItem: Identifiable {
    public let id: String
    public let sourceTitle: String
    public let amountText: String
    public let dateString: String
    public let status: String
    public let utrNumber: String

    public init(id: String, sourceTitle: String, amountText: String, dateString: String, status: String = "Settled", utrNumber: String = "UPI-9482-1092") {
        self.id = id
        self.sourceTitle = sourceTitle
        self.amountText = amountText
        self.dateString = dateString
        self.status = status
        self.utrNumber = utrNumber
    }
}

public struct SettlementLedgerView: View {
    public let onDismiss: () -> Void

    @State private var settlements: [PayoutSettlementItem] = [
        PayoutSettlementItem(id: "set-1", sourceTitle: "Collab Split: AI Super-App Reel with @marcus_v", amountText: "₹4,850", dateString: "Today, 4:30 PM", status: "Settled", utrNumber: "UTR-8492019482"),
        PayoutSettlementItem(id: "set-2", sourceTitle: "Live Stream SuperChat & Kadak Chai Gifts", amountText: "₹1,240", dateString: "Yesterday", status: "Settled", utrNumber: "UTR-7182910482"),
        PayoutSettlementItem(id: "set-3", sourceTitle: "Monthly Insider Subscriptions (14 Members)", amountText: "₹2,786", dateString: "Aug 18, 2026", status: "Settled", utrNumber: "UTR-5910284719"),
        PayoutSettlementItem(id: "set-4", sourceTitle: "Digital Store: Preset Pack Drop", amountText: "₹3,196", dateString: "Aug 15, 2026", status: "Settled", utrNumber: "UTR-3910284918")
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
                    VStack(alignment: .leading, spacing: 18) {
                        // Summary Card
                        VStack(alignment: .leading, spacing: 8) {
                            Text("Total Creator Earnings Settled")
                                .font(.system(size: 12))
                                .foregroundColor(UsColors.textMuted)
                            Text("₹12,072.00")
                                .font(.system(size: 32, weight: .black, design: .rounded))
                                .foregroundColor(UsColors.onlineGreen)
                            Text("Automatic 24/7 instant payout to HDFC Bank **** 9482 via UPI")
                                .font(.system(size: 11))
                                .foregroundColor(UsColors.textMuted)
                        }
                        .padding(18)
                        .frame(maxWidth: .infinity, alignment: .leading)
                        .background(UsColors.bgSecondary)
                        .clipShape(RoundedRectangle(cornerRadius: 18))

                        Text("Settlement Transactions")
                            .font(.system(size: 16, weight: .bold))
                            .foregroundColor(UsColors.textPrimary)

                        LazyVStack(spacing: 12) {
                            ForEach(settlements) { item in
                                settlementRow(item)
                            }
                        }
                    }
                    .padding(16)
                }
            }
            .navigationTitle("Payout Settlements")
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
    private func settlementRow(_ item: PayoutSettlementItem) -> some View {
        VStack(alignment: .leading, spacing: 8) {
            HStack {
                Text(item.sourceTitle)
                    .font(.system(size: 13, weight: .semibold))
                    .foregroundColor(UsColors.textPrimary)
                    .lineLimit(2)

                Spacer()

                Text("+\(item.amountText)")
                    .font(.system(size: 15, weight: .bold, design: .rounded))
                    .foregroundColor(UsColors.onlineGreen)
            }

            HStack {
                Text(item.dateString)
                    .font(.system(size: 11))
                    .foregroundColor(UsColors.textMuted)

                Spacer()

                Text(item.utrNumber)
                    .font(.system(size: 10, weight: .monospaced))
                    .foregroundColor(UsColors.textDim)
            }
        }
        .padding(14)
        .background(UsColors.bgSecondary)
        .clipShape(RoundedRectangle(cornerRadius: 14))
    }
}
