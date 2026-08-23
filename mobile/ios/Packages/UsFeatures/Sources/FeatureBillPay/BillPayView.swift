import SwiftUI
import UsModel
import UsDesignSystem
import UsNetwork

public struct BillCategory: Identifiable {
    public let id: String
    public let title: String
    public let icon: String
    public let color: Color
}

public struct BillPayView: View {
    public let onDismiss: () -> Void

    private let categories: [BillCategory] = [
        BillCategory(id: "elec", title: "Electricity", icon: "bolt.fill", color: Color.yellow),
        BillCategory(id: "mob", title: "Mobile", icon: "iphone", color: UsColors.postbookPrimary),
        BillCategory(id: "dth", title: "DTH", icon: "tv.fill", color: UsColors.posttubePrimary),
        BillCategory(id: "fastag", title: "FASTag", icon: "car.fill", color: UsColors.onlineGreen),
        BillCategory(id: "water", title: "Water", icon: "drop.fill", color: Color.cyan),
        BillCategory(id: "wifi", title: "Broadband", icon: "wifi", color: UsColors.postgramPrimary)
    ]

    @State private var selectedCategory: BillCategory? = nil
    @State private var consumerNumber: String = ""
    @State private var isPaying: Bool = false

    public init(onDismiss: @escaping () -> Void = {}) {
        self.onDismiss = onDismiss
    }

    private let columns = [
        GridItem(.flexible(), spacing: 14),
        GridItem(.flexible(), spacing: 14),
        GridItem(.flexible(), spacing: 14)
    ]

    public var body: some View {
        NavigationStack {
            ZStack {
                UsColors.bgPrimary
                    .ignoresSafeArea()

                ScrollView {
                    VStack(alignment: .leading, spacing: 20) {
                        Text("Recharge & Pay Bills")
                            .font(.system(size: 18, weight: .bold))
                            .foregroundColor(UsColors.textPrimary)

                        LazyVGrid(columns: columns, spacing: 14) {
                            ForEach(categories) { cat in
                                Button(action: { selectedCategory = cat }) {
                                    VStack(spacing: 10) {
                                        ZStack {
                                            Circle()
                                                .fill(cat.color.opacity(0.15))
                                                .frame(width: 54, height: 54)

                                            Image(systemName: cat.icon)
                                                .font(.system(size: 22))
                                                .foregroundColor(cat.color)
                                        }

                                        Text(cat.title)
                                            .font(.system(size: 12, weight: .medium))
                                            .foregroundColor(UsColors.textPrimary)
                                    }
                                    .frame(maxWidth: .infinity)
                                    .padding(.vertical, 14)
                                    .background(UsColors.bgSecondary)
                                    .clipShape(RoundedRectangle(cornerRadius: 14))
                                }
                                .buttonStyle(.plain)
                            }
                        }

                        // Recent Bills
                        Text("Recent Bills")
                            .font(.system(size: 16, weight: .bold))
                            .foregroundColor(UsColors.textPrimary)

                        VStack(spacing: 10) {
                            recentBillRow(name: "BESCOM Bangalore Electricity", account: "CA 482910392", amount: "₹1,420.00", isDue: true)
                            recentBillRow(name: "Jio Fiber Broadband", account: "080-49201948", amount: "₹1,179.00", isDue: false)
                        }
                    }
                    .padding(16)
                }
            }
            .navigationTitle("Bill Payments")
            .navigationBarTitleDisplayMode(.inline)
            .toolbar {
                ToolbarItem(placement: .cancellationAction) {
                    Button("Close", action: onDismiss)
                        .foregroundColor(UsColors.textMuted)
                }
            }
            .sheet(item: $selectedCategory) { cat in
                billPaymentSheet(cat)
            }
        }
    }

    @ViewBuilder
    private func recentBillRow(name: String, account: String, amount: String, isDue: Bool) -> some View {
        HStack(spacing: 12) {
            VStack(alignment: .leading, spacing: 2) {
                Text(name)
                    .font(.system(size: 14, weight: .semibold))
                    .foregroundColor(UsColors.textPrimary)
                Text(account)
                    .font(.system(size: 11))
                    .foregroundColor(UsColors.textMuted)
            }

            Spacer()

            VStack(alignment: .trailing, spacing: 4) {
                Text(amount)
                    .font(.system(size: 15, weight: .bold, design: .rounded))
                    .foregroundColor(UsColors.textPrimary)

                if isDue {
                    Text("DUE")
                        .font(.system(size: 10, weight: .black))
                        .foregroundColor(UsColors.statusError)
                }
            }
        }
        .padding(14)
        .background(UsColors.bgSecondary)
        .clipShape(RoundedRectangle(cornerRadius: 14))
    }

    @ViewBuilder
    private func billPaymentSheet(_ category: BillCategory) -> some View {
        NavigationStack {
            ZStack {
                UsColors.bgPrimary.ignoresSafeArea()

                VStack(spacing: 20) {
                    VStack(alignment: .leading, spacing: 6) {
                        Text("\(category.title) Consumer ID / Account Number")
                            .font(.system(size: 13, weight: .medium))
                            .foregroundColor(UsColors.textMuted)

                        TextField("Enter 10-digit consumer ID", text: $consumerNumber)
                            .textFieldStyle(.plain)
                            .padding(14)
                            .background(UsColors.bgSecondary)
                            .clipShape(RoundedRectangle(cornerRadius: 12))
                            .foregroundColor(UsColors.textPrimary)
                    }

                    Spacer()

                    Button(action: {
                        isPaying = true
                        DispatchQueue.main.asyncAfter(deadline: .now() + 1.2) {
                            isPaying = false
                            selectedCategory = nil
                            ToastManager.shared.show("\(category.title) Bill Paid via UPI", style: .success)
                        }
                    }) {
                        HStack {
                            Spacer()
                            if isPaying {
                                ProgressView().tint(.black)
                            } else {
                                Text("Fetch Bill & Pay")
                                    .font(.system(size: 16, weight: .bold))
                                    .foregroundColor(.black)
                            }
                            Spacer()
                        }
                        .padding(.vertical, 16)
                        .background(Color.white)
                        .clipShape(RoundedRectangle(cornerRadius: 14))
                    }
                    .disabled(consumerNumber.isEmpty || isPaying)
                }
                .padding(16)
            }
            .navigationTitle("Pay \(category.title)")
            .navigationBarTitleDisplayMode(.inline)
            .toolbar {
                ToolbarItem(placement: .cancellationAction) {
                    Button("Cancel") { selectedCategory = nil }
                        .foregroundColor(UsColors.textMuted)
                }
            }
        }
    }
}
