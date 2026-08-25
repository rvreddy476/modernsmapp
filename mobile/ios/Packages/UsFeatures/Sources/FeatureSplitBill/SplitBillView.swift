import SwiftUI
import UsModel
import UsDesignSystem
import UsNetwork

public struct FriendContact: Identifiable, Hashable {
    public let id: String
    public let name: String
    public let upiId: String
    public let avatarUrl: String?
    public var isSelected: Bool

    public init(id: String, name: String, upiId: String, avatarUrl: String? = nil, isSelected: Bool = false) {
        self.id = id
        self.name = name
        self.upiId = upiId
        self.avatarUrl = avatarUrl
        self.isSelected = isSelected
    }
}

public struct SplitBillView: View {
    public let onDismiss: () -> Void

    @State private var billTitle: String = "Dinner at Burma Burma 🍜"
    @State private var totalAmountText: String = "3600"
    @State private var friends: [FriendContact] = [
        FriendContact(id: "f1", name: "Sarah Chen", upiId: "sarah@upi", isSelected: true),
        FriendContact(id: "f2", name: "Marcus Vance", upiId: "marcus@upi", isSelected: true),
        FriendContact(id: "f3", name: "Aanya Sharma", upiId: "aanya@upi", isSelected: true),
        FriendContact(id: "f4", name: "Dev Patel", upiId: "dev@upi", isSelected: false)
    ]
    @State private var isSendingRequests: Bool = false

    public init(onDismiss: @escaping () -> Void = {}) {
        self.onDismiss = onDismiss
    }

    private var selectedCount: Int {
        // Including self
        friends.filter { $0.isSelected }.count + 1
    }

    private var perPersonAmount: String {
        let total = Double(totalAmountText) ?? 0
        guard selectedCount > 0 else { return "₹0" }
        let perPerson = total / Double(selectedCount)
        return String(format: "₹%.0f", perPerson)
    }

    public var body: some View {
        NavigationStack {
            ZStack {
                UsColors.bgPrimary
                    .ignoresSafeArea()

                ScrollView {
                    VStack(alignment: .leading, spacing: 20) {
                        // Total Bill Input Card
                        billInputCard

                        // Per Person Summary
                        perPersonBanner

                        // Friends Selection
                        Text("Split with Friends (\(selectedCount - 1) selected)")
                            .font(.system(size: 16, weight: .bold))
                            .foregroundColor(UsColors.textPrimary)

                        LazyVStack(spacing: 10) {
                            ForEach($friends) { $friend in
                                friendRow(friend: $friend)
                            }
                        }

                        // Send Split Requests Button
                        Button(action: sendSplitRequests) {
                            HStack {
                                Spacer()
                                if isSendingRequests {
                                    ProgressView().tint(.black)
                                } else {
                                    Text("Send UPI Split Requests (\(perPersonAmount) each)")
                                        .font(.system(size: 15, weight: .bold))
                                        .foregroundColor(.black)
                                }
                                Spacer()
                            }
                            .padding(.vertical, 16)
                            .background(Color.white)
                            .clipShape(RoundedRectangle(cornerRadius: 14))
                        }
                        .disabled(isSendingRequests || (Double(totalAmountText) ?? 0) <= 0)
                        .padding(.top, 12)
                    }
                    .padding(16)
                }
            }
            .navigationTitle("Split Bill")
            .navigationBarTitleDisplayMode(.inline)
            .toolbar {
                ToolbarItem(placement: .cancellationAction) {
                    Button("Close", action: onDismiss)
                        .foregroundColor(UsColors.textMuted)
                }
            }
        }
    }

    private var billInputCard: some View {
        VStack(alignment: .leading, spacing: 14) {
            TextField("Bill Description (e.g. Dinner, Uber)", text: $billTitle)
                .font(.system(size: 16, weight: .semibold))
                .foregroundColor(UsColors.textPrimary)

            HStack(spacing: 8) {
                Text("₹")
                    .font(.system(size: 32, weight: .bold))
                    .foregroundColor(UsColors.textMuted)

                TextField("0", text: $totalAmountText)
                    .font(.system(size: 38, weight: .black, design: .rounded))
                    .foregroundColor(UsColors.textPrimary)
                    .keyboardType(.numberPad)
            }
        }
        .padding(18)
        .background(UsColors.bgSecondary)
        .clipShape(RoundedRectangle(cornerRadius: 16))
    }

    private var perPersonBanner: some View {
        HStack {
            VStack(alignment: .leading, spacing: 2) {
                Text("Each Person Owes")
                    .font(.system(size: 12))
                    .foregroundColor(UsColors.textMuted)
                Text(perPersonAmount)
                    .font(.system(size: 22, weight: .bold, design: .rounded))
                    .foregroundColor(UsColors.onlineGreen)
            }

            Spacer()

            Text("Split equally between \(selectedCount) people")
                .font(.system(size: 12, weight: .medium))
                .foregroundColor(UsColors.textMuted)
        }
        .padding(14)
        .background(UsColors.bgTertiary)
        .clipShape(RoundedRectangle(cornerRadius: 12))
    }

    @ViewBuilder
    private func friendRow(friend: Binding<FriendContact>) -> some View {
        Button(action: {
            friend.wrappedValue.isSelected.toggle()
            HapticManager.shared.trigger(.selection)
        }) {
            HStack(spacing: 12) {
                UsAvatar(name: friend.wrappedValue.name, size: .small)

                VStack(alignment: .leading, spacing: 2) {
                    Text(friend.wrappedValue.name)
                        .font(.system(size: 14, weight: .semibold))
                        .foregroundColor(UsColors.textPrimary)
                    Text(friend.wrappedValue.upiId)
                        .font(.system(size: 12))
                        .foregroundColor(UsColors.textMuted)
                }

                Spacer()

                Image(systemName: friend.wrappedValue.isSelected ? "checkmark.circle.fill" : "circle")
                    .font(.system(size: 22))
                    .foregroundColor(friend.wrappedValue.isSelected ? UsColors.postbookPrimary : UsColors.textDim)
            }
            .padding(12)
            .background(UsColors.bgSecondary)
            .clipShape(RoundedRectangle(cornerRadius: 12))
        }
        .buttonStyle(.plain)
    }

    private func sendSplitRequests() {
        isSendingRequests = true
        DispatchQueue.main.asyncAfter(deadline: .now() + 1.2) {
            isSendingRequests = false
            HapticManager.shared.trigger(.success)
            ToastManager.shared.show("UPI Split Requests Sent to \(selectedCount - 1) Friends!", style: .success)
            onDismiss()
        }
    }
}
