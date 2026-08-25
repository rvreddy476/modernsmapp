import SwiftUI
import UsModel
import UsDesignSystem
import UsNetwork

public struct MetroCity: Identifiable {
    public let id: String
    public let name: String
    public let cardName: String
    public let color: Color

    public init(id: String, name: String, cardName: String, color: Color) {
        self.id = id
        self.name = name
        self.cardName = cardName
        self.color = color
    }
}

public struct MetroRechargeView: View {
    public let onDismiss: () -> Void

    @State private var cardNumber: String = "4820 9481 0293"
    @State private var selectedAmount: String = "200"
    @State private var selectedCityId: String = "bgl"
    @State private var isRecharging: Bool = false

    private let cities: [MetroCity] = [
        MetroCity(id: "bgl", name: "Bengaluru", cardName: "Namma Metro (BMRCL)", color: Color.purple),
        MetroCity(id: "del", name: "Delhi NCR", cardName: "Delhi Metro (DMRC)", color: Color.red),
        MetroCity(id: "mum", name: "Mumbai", cardName: "Maha Metro Card", color: Color.blue),
        MetroCity(id: "hyd", name: "Hyderabad", cardName: "L&T Metro Smart Card", color: Color.green)
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
                    VStack(alignment: .leading, spacing: 20) {
                        // City Selector
                        Text("Select Transit Authority")
                            .font(.system(size: 14, weight: .bold))
                            .foregroundColor(UsColors.textPrimary)

                        ScrollView(.horizontal, showsIndicators: false) {
                            HStack(spacing: 10) {
                                ForEach(cities) { city in
                                    let isSelected = selectedCityId == city.id
                                    Button(action: {
                                        selectedCityId = city.id
                                        HapticManager.shared.trigger(.selection)
                                    }) {
                                        VStack(alignment: .leading, spacing: 4) {
                                            Text(city.name)
                                                .font(.system(size: 14, weight: .bold))
                                                .foregroundColor(isSelected ? .black : UsColors.textPrimary)
                                            Text(city.cardName)
                                                .font(.system(size: 11))
                                                .foregroundColor(isSelected ? Color.black.opacity(0.8) : UsColors.textMuted)
                                        }
                                        .padding(14)
                                        .background(isSelected ? Color.white : UsColors.bgSecondary)
                                        .clipShape(RoundedRectangle(cornerRadius: 14))
                                    }
                                    .buttonStyle(.plain)
                                }
                            }
                        }

                        // Smart Card Input
                        VStack(alignment: .leading, spacing: 8) {
                            Text("Metro Smart Card Number")
                                .font(.system(size: 13, weight: .semibold))
                                .foregroundColor(UsColors.textPrimary)

                            TextField("Enter 12-digit card number", text: $cardNumber)
                                .textFieldStyle(.plain)
                                .font(.system(size: 16, weight: .bold, design: .monospaced))
                                .padding(14)
                                .background(UsColors.bgSecondary)
                                .clipShape(RoundedRectangle(cornerRadius: 12))
                                .foregroundColor(UsColors.textPrimary)
                        }

                        // Recharge Amount Chips
                        VStack(alignment: .leading, spacing: 10) {
                            Text("Select Top-Up Amount")
                                .font(.system(size: 14, weight: .bold))
                                .foregroundColor(UsColors.textPrimary)

                            HStack(spacing: 10) {
                                ForEach(["100", "200", "500", "1000"], id: \.self) { amt in
                                    let isSelected = selectedAmount == amt
                                    Button(action: {
                                        selectedAmount = amt
                                        HapticManager.shared.trigger(.selection)
                                    }) {
                                        Text("₹\(amt)")
                                            .font(.system(size: 15, weight: .bold, design: .rounded))
                                            .foregroundColor(isSelected ? .black : UsColors.textPrimary)
                                            .padding(.horizontal, 16)
                                            .padding(.vertical, 10)
                                            .background(isSelected ? Color.white : UsColors.bgSecondary)
                                            .clipShape(Capsule())
                                    }
                                    .buttonStyle(.plain)
                                }
                            }
                        }

                        Spacer()

                        // Recharge Button
                        Button(action: rechargeCard) {
                            HStack {
                                Spacer()
                                if isRecharging {
                                    ProgressView().tint(.black)
                                } else {
                                    Text("Recharge ₹\(selectedAmount) via US Wallet / UPI")
                                        .font(.system(size: 15, weight: .bold))
                                        .foregroundColor(.black)
                                }
                                Spacer()
                            }
                            .padding(.vertical, 16)
                            .background(Color.white)
                            .clipShape(RoundedRectangle(cornerRadius: 14))
                        }
                        .disabled(isRecharging || cardNumber.isEmpty)
                    }
                    .padding(16)
                }
            }
            .navigationTitle("Metro Recharge")
            .navigationBarTitleDisplayMode(.inline)
            .toolbar {
                ToolbarItem(placement: .cancellationAction) {
                    Button("Close", action: onDismiss)
                        .foregroundColor(UsColors.textMuted)
                }
            }
        }
    }

    private func rechargeCard() {
        isRecharging = true
        DispatchQueue.main.asyncAfter(deadline: .now() + 1.2) {
            isRecharging = false
            HapticManager.shared.trigger(.success)
            ToastManager.shared.show("🎉 ₹\(selectedAmount) Metro Card Top-Up Successful!", style: .success)
            onDismiss()
        }
    }
}
