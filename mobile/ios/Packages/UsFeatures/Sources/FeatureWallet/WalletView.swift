import SwiftUI
import UsModel
import UsDesignSystem
import UsNetwork

@Observable
public final class WalletViewModel: @unchecked Sendable {
    public var balance: WalletBalance = WalletBalance()
    public var transactions: [TransactionItem] = []
    public var isLoading: Bool = false
    public var errorMessage: String? = nil

    private let client: APIClientProtocol

    public init(client: APIClientProtocol = APIClient()) {
        self.client = client
        populateMockTransactions()
    }

    @MainActor
    public func loadWallet() async {
        isLoading = true
        errorMessage = nil
        do {
            let res: WalletBalance = try await client.request(endpoint: "v1/wallet/balance", method: "GET", query: nil, body: nil)
            self.balance = res
        } catch {
            // Keep default balance if endpoint unpopulated
        }
        self.isLoading = false
    }

    private func populateMockTransactions() {
        transactions = [
            TransactionItem(id: "tx-1", type: .receive, title: "Received from Sarah", counterparty: "sarah@us", amountPaise: 120000, formattedAmount: "+₹1,200.00", isCredit: true, timestamp: "Today, 2:15 PM"),
            TransactionItem(id: "tx-2", type: .merchant, title: "Blue Tokai Coffee", counterparty: "bluetokai@upi", amountPaise: 38000, formattedAmount: "-₹380.00", isCredit: false, timestamp: "Yesterday, 11:30 AM"),
            TransactionItem(id: "tx-3", type: .topup, title: "Bank Top-up (HDFC)", counterparty: "HDFC Bank ••4821", amountPaise: 200000, formattedAmount: "+₹2,000.00", isCredit: true, timestamp: "Aug 19, 6:45 PM"),
            TransactionItem(id: "tx-4", type: .send, title: "Sent to Marcus", counterparty: "marcus@us", amountPaise: 50000, formattedAmount: "-₹500.00", isCredit: false, timestamp: "Aug 18, 9:20 PM")
        ]
    }
}

public struct WalletView: View {
    @State private var viewModel: WalletViewModel
    @State private var showSendSheet: Bool = false
    @State private var showAddMoneySheet: Bool = false

    public init(client: APIClientProtocol = APIClient()) {
        _viewModel = State(initialValue: WalletViewModel(client: client))
    }

    public var body: some View {
        NavigationStack {
            ZStack {
                UsColors.bgPrimary
                    .ignoresSafeArea()

                ScrollView {
                    VStack(spacing: 20) {
                        // 1. Sleek Gradient Balance Card
                        balanceCard

                        // 2. Quick Actions Row
                        quickActionsRow

                        // 3. Recent Transactions
                        transactionsSection
                    }
                    .padding(16)
                }
            }
            .navigationTitle("US Wallet")
            .navigationBarTitleDisplayMode(.inline)
            .sheet(isPresented: $showSendSheet) {
                SendMoneyView {
                    showSendSheet = false
                }
            }
        }
    }

    private var balanceCard: some View {
        VStack(alignment: .leading, spacing: 16) {
            HStack {
                VStack(alignment: .leading, spacing: 4) {
                    Text("Total Balance")
                        .font(.system(size: 13, weight: .medium))
                        .foregroundColor(.white.opacity(0.8))
                    Text(viewModel.balance.formattedBalance)
                        .font(.system(size: 32, weight: .black, design: .rounded))
                        .foregroundColor(.white)
                }

                Spacer()

                Image(systemName: "creditcard.fill")
                    .font(.system(size: 28))
                    .foregroundColor(.white.opacity(0.9))
            }

            Divider().background(Color.white.opacity(0.2))

            HStack {
                VStack(alignment: .leading, spacing: 2) {
                    Text("UPI ID")
                        .font(.system(size: 11))
                        .foregroundColor(.white.opacity(0.7))
                    Text(viewModel.balance.upiId)
                        .font(.system(size: 13, weight: .bold))
                        .foregroundColor(.white)
                }

                Spacer()

                HStack(spacing: 4) {
                    Image(systemName: "checkmark.shield.fill")
                        .foregroundColor(UsColors.onlineGreen)
                        .font(.system(size: 12))
                    Text("NPCI Verified")
                        .font(.system(size: 11, weight: .semibold))
                        .foregroundColor(.white)
                }
                .padding(.horizontal, 8)
                .padding(.vertical, 4)
                .background(Color.white.opacity(0.15))
                .clipShape(Capsule())
            }
        }
        .padding(20)
        .background(
            LinearGradient(
                colors: [Color(red: 0x1E/255.0, green: 0x3C/255.0, blue: 0x72/255.0),
                         Color(red: 0x2A/255.0, green: 0x52/255.0, blue: 0x98/255.0)],
                startPoint: .topLeading,
                endPoint: .bottomTrailing
            )
        )
        .clipShape(RoundedRectangle(cornerRadius: 20))
        .shadow(color: Color.blue.opacity(0.3), radius: 16, x: 0, y: 8)
    }

    private var quickActionsRow: some View {
        HStack(spacing: 12) {
            quickActionButton(icon: "qrcode.viewfinder", title: "Scan QR") {
                // QR Scanner
            }

            quickActionButton(icon: "paperplane.fill", title: "Send") {
                showSendSheet = true
            }

            quickActionButton(icon: "plus.circle.fill", title: "Add Money") {
                // Add money
            }

            quickActionButton(icon: "arrow.left.arrow.right", title: "Transfer") {
                // Self transfer
            }
        }
    }

    private func quickActionButton(icon: String, title: String, action: @escaping () -> Void) -> some View {
        Button(action: action) {
            VStack(spacing: 8) {
                ZStack {
                    Circle()
                        .fill(UsColors.bgSecondary)
                        .frame(width: 52, height: 52)
                        .overlay(Circle().stroke(UsColors.borderMedium, lineWidth: 1))

                    Image(systemName: icon)
                        .font(.system(size: 20))
                        .foregroundColor(UsColors.postbookPrimary)
                }

                Text(title)
                    .font(.system(size: 12, weight: .medium))
                    .foregroundColor(UsColors.textPrimary)
            }
            .frame(maxWidth: .infinity)
        }
        .buttonStyle(.plain)
    }

    private var transactionsSection: some View {
        VStack(alignment: .leading, spacing: 12) {
            HStack {
                Text("Recent Transactions")
                    .font(.system(size: 16, weight: .bold))
                    .foregroundColor(UsColors.textPrimary)
                Spacer()
                Button("See All") {}
                    .font(.system(size: 13, weight: .semibold))
                    .foregroundColor(UsColors.postbookPrimary)
            }

            LazyVStack(spacing: 10) {
                ForEach(viewModel.transactions) { tx in
                    transactionRow(tx)
                }
            }
        }
    }

    @ViewBuilder
    private func transactionRow(_ tx: TransactionItem) -> some View {
        HStack(spacing: 12) {
            ZStack {
                Circle()
                    .fill(tx.isCredit ? UsColors.onlineGreen.opacity(0.15) : UsColors.bgTertiary)
                    .frame(width: 44, height: 44)

                Image(systemName: tx.isCredit ? "arrow.down.left" : "arrow.up.right")
                    .font(.system(size: 16, weight: .bold))
                    .foregroundColor(tx.isCredit ? UsColors.onlineGreen : UsColors.textPrimary)
            }

            VStack(alignment: .leading, spacing: 2) {
                Text(tx.title)
                    .font(.system(size: 14, weight: .semibold))
                    .foregroundColor(UsColors.textPrimary)
                Text("\(tx.counterparty) • \(tx.timestamp)")
                    .font(.system(size: 11))
                    .foregroundColor(UsColors.textMuted)
            }

            Spacer()

            Text(tx.formattedAmount)
                .font(.system(size: 15, weight: .bold, design: .rounded))
                .foregroundColor(tx.isCredit ? UsColors.onlineGreen : UsColors.textPrimary)
        }
        .padding(12)
        .background(UsColors.bgSecondary)
        .clipShape(RoundedRectangle(cornerRadius: 14))
    }
}

public struct SendMoneyView: View {
    @State private var recipient: String = ""
    @State private var amount: String = ""
    @State private var note: String = ""
    @State private var isProcessing: Bool = false
    public let onDismiss: () -> Void

    public init(onDismiss: @escaping () -> Void = {}) {
        self.onDismiss = onDismiss
    }

    public var body: some View {
        NavigationStack {
            ZStack {
                UsColors.bgPrimary
                    .ignoresSafeArea()

                VStack(spacing: 20) {
                    VStack(alignment: .leading, spacing: 6) {
                        Text("Send To")
                            .font(.system(size: 13, weight: .medium))
                            .foregroundColor(UsColors.textMuted)
                        TextField("Username, Mobile number, or UPI ID", text: $recipient)
                            .textFieldStyle(.plain)
                            .padding(14)
                            .background(UsColors.bgSecondary)
                            .clipShape(RoundedRectangle(cornerRadius: 12))
                            .foregroundColor(UsColors.textPrimary)
                    }

                    VStack(alignment: .leading, spacing: 6) {
                        Text("Amount")
                            .font(.system(size: 13, weight: .medium))
                            .foregroundColor(UsColors.textMuted)
                        HStack {
                            Text("₹")
                                .font(.system(size: 28, weight: .bold))
                                .foregroundColor(UsColors.textPrimary)
                            TextField("0", text: $amount)
                                .keyboardType(.decimalPad)
                                .font(.system(size: 28, weight: .bold, design: .rounded))
                                .foregroundColor(UsColors.textPrimary)
                        }
                        .padding(14)
                        .background(UsColors.bgSecondary)
                        .clipShape(RoundedRectangle(cornerRadius: 12))
                    }

                    VStack(alignment: .leading, spacing: 6) {
                        Text("Note (Optional)")
                            .font(.system(size: 13, weight: .medium))
                            .foregroundColor(UsColors.textMuted)
                        TextField("What's this for?", text: $note)
                            .textFieldStyle(.plain)
                            .padding(14)
                            .background(UsColors.bgSecondary)
                            .clipShape(RoundedRectangle(cornerRadius: 12))
                            .foregroundColor(UsColors.textPrimary)
                    }

                    Spacer()

                    Button(action: {
                        isProcessing = true
                        DispatchQueue.main.asyncAfter(deadline: .now() + 1.0) {
                            isProcessing = false
                            ToastManager.shared.show("₹\(amount) Sent Successfully", style: .success)
                            onDismiss()
                        }
                    }) {
                        HStack {
                            Spacer()
                            if isProcessing {
                                ProgressView().tint(.black)
                            } else {
                                Text("Pay Securely")
                                    .font(.system(size: 16, weight: .bold))
                                    .foregroundColor(.black)
                            }
                            Spacer()
                        }
                        .padding(.vertical, 16)
                        .background(Color.white)
                        .clipShape(RoundedRectangle(cornerRadius: 14))
                    }
                    .disabled(recipient.isEmpty || amount.isEmpty || isProcessing)
                }
                .padding(16)
            }
            .navigationTitle("Send Money")
            .navigationBarTitleDisplayMode(.inline)
            .toolbar {
                ToolbarItem(placement: .cancellationAction) {
                    Button("Cancel", action: onDismiss)
                        .foregroundColor(UsColors.textMuted)
                }
            }
        }
    }
}
