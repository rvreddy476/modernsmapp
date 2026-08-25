import SwiftUI
import UsModel
import UsDesignSystem
import UsNetwork

public struct DigitalGoldView: View {
    public let onDismiss: () -> Void

    @State private var goldPricePerGram: Double = 7142.50
    @State private var userGoldGrams: Double = 0.428
    @State private var buyAmountText: String = "100"
    @State private var isPurchasing: Bool = false

    public init(onDismiss: @escaping () -> Void = {}) {
        self.onDismiss = onDismiss
    }

    private var currentVaultValueString: String {
        let val = userGoldGrams * goldPricePerGram
        return String(format: "₹%.2f", val)
    }

    private var calculatedGramsForBuy: String {
        let amount = Double(buyAmountText) ?? 0
        let grams = amount / goldPricePerGram
        return String(format: "%.4f gm", grams)
    }

    public var body: some View {
        NavigationStack {
            ZStack {
                UsColors.bgPrimary
                    .ignoresSafeArea()

                ScrollView {
                    VStack(spacing: 20) {
                        // Vault Balance Card
                        vaultBalanceCard

                        // Live Gold Price Ticker
                        priceTickerCard

                        // Quick Buy Input
                        quickBuyCard

                        Spacer()

                        // Buy Gold Button
                        Button(action: buyGold) {
                            HStack {
                                Spacer()
                                if isPurchasing {
                                    ProgressView().tint(.black)
                                } else {
                                    Text("Buy 24K Gold for ₹\(buyAmountText) (UPI)")
                                        .font(.system(size: 15, weight: .bold))
                                        .foregroundColor(.black)
                                }
                                Spacer()
                            }
                            .padding(.vertical, 16)
                            .background(Color.yellow)
                            .clipShape(RoundedRectangle(cornerRadius: 14))
                        }
                        .disabled(isPurchasing || (Double(buyAmountText) ?? 0) <= 0)
                    }
                    .padding(16)
                }
            }
            .navigationTitle("24K Digital Gold")
            .navigationBarTitleDisplayMode(.inline)
            .toolbar {
                ToolbarItem(placement: .cancellationAction) {
                    Button("Close", action: onDismiss)
                        .foregroundColor(UsColors.textMuted)
                }
            }
        }
    }

    private var vaultBalanceCard: some View {
        VStack(alignment: .leading, spacing: 8) {
            Text("Your 24K 99.9% Pure Gold Vault")
                .font(.system(size: 12))
                .foregroundColor(Color.black.opacity(0.8))

            HStack {
                Text(currentVaultValueString)
                    .font(.system(size: 28, weight: .black, design: .rounded))
                    .foregroundColor(Color.black)

                Spacer()

                Text(String(format: "%.4f gm", userGoldGrams))
                    .font(.system(size: 14, weight: .bold))
                    .foregroundColor(Color.black.opacity(0.9))
                    .padding(.horizontal, 10)
                    .padding(.vertical, 4)
                    .background(Color.black.opacity(0.1))
                    .clipShape(Capsule())
            }
        }
        .padding(18)
        .background(
            LinearGradient(
                colors: [Color.yellow, Color.orange.opacity(0.8)],
                startPoint: .topLeading,
                endPoint: .bottomTrailing
            )
        )
        .clipShape(RoundedRectangle(cornerRadius: 18))
    }

    private var priceTickerCard: some View {
        HStack {
            VStack(alignment: .leading, spacing: 2) {
                Text("Live Market Price")
                    .font(.system(size: 12))
                    .foregroundColor(UsColors.textMuted)
                Text(String(format: "₹%.2f / gm", goldPricePerGram))
                    .font(.system(size: 18, weight: .bold, design: .rounded))
                    .foregroundColor(UsColors.textPrimary)
            }

            Spacer()

            HStack(spacing: 4) {
                Image(systemName: "arrow.up.right")
                Text("+1.42% Today")
            }
            .font(.system(size: 12, weight: .bold))
            .foregroundColor(UsColors.onlineGreen)
            .padding(.horizontal, 10)
            .padding(.vertical, 5)
            .background(UsColors.onlineGreen.opacity(0.15))
            .clipShape(Capsule())
        }
        .padding(14)
        .background(UsColors.bgSecondary)
        .clipShape(RoundedRectangle(cornerRadius: 14))
    }

    private var quickBuyCard: some View {
        VStack(alignment: .leading, spacing: 14) {
            Text("Buy Gold (Starts from ₹10)")
                .font(.system(size: 14, weight: .bold))
                .foregroundColor(UsColors.textPrimary)

            HStack(spacing: 8) {
                Text("₹")
                    .font(.system(size: 28, weight: .bold))
                    .foregroundColor(UsColors.textMuted)

                TextField("100", text: $buyAmountText)
                    .font(.system(size: 32, weight: .black, design: .rounded))
                    .foregroundColor(UsColors.textPrimary)
                    .keyboardType(.numberPad)
            }

            Text("You will receive: \(calculatedGramsForBuy) of 24K Gold")
                .font(.system(size: 12))
                .foregroundColor(UsColors.onlineGreen)

            // Preset Amount Chips
            HStack(spacing: 8) {
                ForEach(["50", "100", "500", "1000"], id: \.self) { amt in
                    Button(action: { buyAmountText = amt }) {
                        Text("₹\(amt)")
                            .font(.system(size: 12, weight: .bold))
                            .foregroundColor(buyAmountText == amt ? .black : UsColors.textPrimary)
                            .padding(.horizontal, 12)
                            .padding(.vertical, 6)
                            .background(buyAmountText == amt ? Color.yellow : UsColors.bgTertiary)
                            .clipShape(Capsule())
                    }
                    .buttonStyle(.plain)
                }
            }
        }
        .padding(16)
        .background(UsColors.bgSecondary)
        .clipShape(RoundedRectangle(cornerRadius: 16))
    }

    private func buyGold() {
        isPurchasing = true
        let amount = Double(buyAmountText) ?? 0
        let addedGrams = amount / goldPricePerGram

        DispatchQueue.main.asyncAfter(deadline: .now() + 1.2) {
            isPurchasing = false
            userGoldGrams += addedGrams
            HapticManager.shared.trigger(.success)
            ToastManager.shared.show("🎉 \(String(format: "%.4f", addedGrams)) gm 24K Gold added to your vault!", style: .success)
        }
    }
}
