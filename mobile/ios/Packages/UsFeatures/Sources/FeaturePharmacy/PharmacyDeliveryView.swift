import SwiftUI
import UsModel
import UsDesignSystem
import UsNetwork

public struct MedicineItem: Identifiable, Hashable {
    public let id: String
    public let name: String
    public let dosage: String
    public let price: String
    public let requiresPrescription: Bool
    public let category: String

    public init(id: String, name: String, dosage: String, price: String, requiresPrescription: Bool = false, category: String = "Essentials") {
        self.id = id
        self.name = name
        self.dosage = dosage
        self.price = price
        self.requiresPrescription = requiresPrescription
        self.category = category
    }
}

public struct PharmacyDeliveryView: View {
    public let onDismiss: () -> Void

    @State private var medicines: [MedicineItem] = [
        MedicineItem(id: "med-1", name: "Dolo 650mg Paracetamol", dosage: "Strip of 15 Tablets", price: "₹32"),
        MedicineItem(id: "med-2", name: "Volini Pain Relief Gel", dosage: "50g Tube", price: "₹145"),
        MedicineItem(id: "med-3", name: "Allegra 120mg Antihistamine", dosage: "Strip of 10 Tablets", price: "₹198", requiresPrescription: true),
        MedicineItem(id: "med-4", name: "Digene Acidity Relief Liquid", dosage: "200ml Mint Flavor", price: "₹130")
    ]
    @State private var cartItems: [MedicineItem] = []
    @State private var isPlacingOrder: Bool = false

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
                        // 10-Min Delivery Promise Banner
                        HStack(spacing: 12) {
                            ZStack {
                                Circle().fill(UsColors.onlineGreen.opacity(0.2)).frame(width: 44, height: 44)
                                Image(systemName: "cross.case.fill")
                                    .foregroundColor(UsColors.onlineGreen)
                                    .font(.system(size: 20))
                            }

                            VStack(alignment: .leading, spacing: 2) {
                                Text("10-Minute Pharmacy Delivery ⚡️")
                                    .font(.system(size: 15, weight: .bold))
                                    .foregroundColor(UsColors.textPrimary)
                                Text("Verified pharmacists • Free delivery above ₹99")
                                    .font(.system(size: 11))
                                    .foregroundColor(UsColors.textMuted)
                            }
                        }
                        .padding(14)
                        .frame(maxWidth: .infinity, alignment: .leading)
                        .background(UsColors.bgSecondary)
                        .clipShape(RoundedRectangle(cornerRadius: 16))

                        Text("Popular OTC Medicines & First Aid")
                            .font(.system(size: 15, weight: .bold))
                            .foregroundColor(UsColors.textPrimary)

                        LazyVStack(spacing: 10) {
                            ForEach(medicines) { med in
                                medicineRow(med)
                            }
                        }

                        if !cartItems.isEmpty {
                            Button(action: placeOrder) {
                                HStack {
                                    Spacer()
                                    if isPlacingOrder {
                                        ProgressView().tint(.black)
                                    } else {
                                        Text("Order \(cartItems.count) Items (10-Min Delivery)")
                                            .font(.system(size: 15, weight: .bold))
                                            .foregroundColor(.black)
                                    }
                                    Spacer()
                                }
                                .padding(.vertical, 16)
                                .background(Color.white)
                                .clipShape(RoundedRectangle(cornerRadius: 14))
                            }
                            .disabled(isPlacingOrder)
                            .padding(.top, 12)
                        }
                    }
                    .padding(16)
                }
            }
            .navigationTitle("Pharmacy & Care")
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
    private func medicineRow(_ med: MedicineItem) -> some View {
        let inCart = cartItems.contains { $0.id == med.id }
        HStack(spacing: 12) {
            VStack(alignment: .leading, spacing: 2) {
                HStack(spacing: 6) {
                    Text(med.name)
                        .font(.system(size: 14, weight: .bold))
                        .foregroundColor(UsColors.textPrimary)

                    if med.requiresPrescription {
                        Text("Rx")
                            .font(.system(size: 9, weight: .black))
                            .foregroundColor(.white)
                            .padding(.horizontal, 4)
                            .padding(.vertical, 2)
                            .background(UsColors.liveRed)
                            .clipShape(RoundedRectangle(cornerRadius: 4))
                    }
                }

                Text(med.dosage)
                    .font(.system(size: 11))
                    .foregroundColor(UsColors.textMuted)

                Text(med.price)
                    .font(.system(size: 15, weight: .black, design: .rounded))
                    .foregroundColor(UsColors.onlineGreen)
                    .padding(.top, 2)
            }

            Spacer()

            Button(action: {
                if inCart {
                    cartItems.removeAll { $0.id == med.id }
                    HapticManager.shared.trigger(.light)
                } else {
                    cartItems.append(med)
                    HapticManager.shared.trigger(.success)
                }
            }) {
                Text(inCart ? "Added ✓" : "+ Add")
                    .font(.system(size: 12, weight: .bold))
                    .foregroundColor(inCart ? .white : .black)
                    .padding(.horizontal, 14)
                    .padding(.vertical, 8)
                    .background(inCart ? UsColors.bgTertiary : Color.white)
                    .clipShape(Capsule())
            }
        }
        .padding(14)
        .background(UsColors.bgSecondary)
        .clipShape(RoundedRectangle(cornerRadius: 14))
    }

    private func placeOrder() {
        isPlacingOrder = true
        DispatchQueue.main.asyncAfter(deadline: .now() + 1.2) {
            isPlacingOrder = false
            HapticManager.shared.trigger(.success)
            ToastManager.shared.show("🎉 Pharmacy Order Placed! Delivery in 10 mins 🛵", style: .success)
            onDismiss()
        }
    }
}
