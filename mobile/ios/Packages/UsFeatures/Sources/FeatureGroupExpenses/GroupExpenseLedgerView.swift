import SwiftUI
import UsModel
import UsDesignSystem
import UsNetwork

public struct GroupDebtItem: Identifiable {
    public let id: String
    public let fromName: String
    public let toName: String
    public let amountText: String
    public let reason: String

    public init(id: String, fromName: String, toName: String, amountText: String, reason: String) {
        self.id = id
        self.fromName = fromName
        self.toName = toName
        self.amountText = amountText
        self.reason = reason
    }
}

public struct GroupExpenseLedgerView: View {
    public let groupName: String
    public let onDismiss: () -> Void

    @State private var debts: [GroupDebtItem] = [
        GroupDebtItem(id: "gd-1", fromName: "You", toName: "Sarah Chen", amountText: "₹850", reason: "Goa Villa Airbnb booking"),
        GroupDebtItem(id: "gd-2", fromName: "Marcus Vance", toName: "You", amountText: "₹420", reason: "Sunday Brunch bill"),
        GroupDebtItem(id: "gd-3", fromName: "Aanya Sharma", toName: "You", amountText: "₹650", reason: "Uber XL ride from airport")
    ]

    public init(
        groupName: String = "Goa Roadtrip 2026 🌴",
        onDismiss: @escaping () -> Void = {}
    ) {
        self.groupName = groupName
        self.onDismiss = onDismiss
    }

    public var body: some View {
        NavigationStack {
            ZStack {
                UsColors.bgPrimary
                    .ignoresSafeArea()

                ScrollView {
                    VStack(alignment: .leading, spacing: 18) {
                        // Net Balance Header
                        VStack(alignment: .leading, spacing: 6) {
                            Text("Your Net Group Balance")
                                .font(.system(size: 12))
                                .foregroundColor(UsColors.textMuted)

                            Text("+₹220.00")
                                .font(.system(size: 28, weight: .black, design: .rounded))
                                .foregroundColor(UsColors.onlineGreen)

                            Text("You are owed ₹1,070 in total • You owe ₹850")
                                .font(.system(size: 12))
                                .foregroundColor(UsColors.textSecondary)
                        }
                        .padding(18)
                        .frame(maxWidth: .infinity, alignment: .leading)
                        .background(UsColors.bgSecondary)
                        .clipShape(RoundedRectangle(cornerRadius: 18))

                        Text("Optimal Debt Settlements")
                            .font(.system(size: 15, weight: .bold))
                            .foregroundColor(UsColors.textPrimary)

                        LazyVStack(spacing: 12) {
                            ForEach(debts) { debt in
                                debtRow(debt)
                            }
                        }
                    }
                    .padding(16)
                }
            }
            .navigationTitle(groupName)
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
    private func debtRow(_ debt: GroupDebtItem) -> some View {
        let isOwedByMe = debt.fromName == "You"
        HStack(spacing: 12) {
            VStack(alignment: .leading, spacing: 2) {
                HStack(spacing: 4) {
                    Text(debt.fromName)
                        .font(.system(size: 13, weight: .bold))
                        .foregroundColor(UsColors.textPrimary)
                    Text("owes")
                        .font(.system(size: 12))
                        .foregroundColor(UsColors.textMuted)
                    Text(debt.toName)
                        .font(.system(size: 13, weight: .bold))
                        .foregroundColor(UsColors.textPrimary)
                }

                Text(debt.reason)
                    .font(.system(size: 11))
                    .foregroundColor(UsColors.textMuted)
            }

            Spacer()

            VStack(alignment: .trailing, spacing: 4) {
                Text(debt.amountText)
                    .font(.system(size: 16, weight: .black, design: .rounded))
                    .foregroundColor(isOwedByMe ? UsColors.liveRed : UsColors.onlineGreen)

                if isOwedByMe {
                    Button(action: {
                        HapticManager.shared.trigger(.success)
                        ToastManager.shared.show("Paid \(debt.amountText) to \(debt.toName) via UPI!", style: .success)
                    }) {
                        Text("Settle Up")
                            .font(.system(size: 11, weight: .bold))
                            .foregroundColor(.black)
                            .padding(.horizontal, 10)
                            .padding(.vertical, 4)
                            .background(Color.white)
                            .clipShape(Capsule())
                    }
                }
            }
        }
        .padding(14)
        .background(UsColors.bgSecondary)
        .clipShape(RoundedRectangle(cornerRadius: 14))
    }
}
