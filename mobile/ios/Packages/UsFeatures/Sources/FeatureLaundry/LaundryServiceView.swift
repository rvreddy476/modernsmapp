import SwiftUI
import UsModel
import UsDesignSystem
import UsNetwork

public struct LaundryServiceOption: Identifiable {
    public let id: String
    public let title: String
    public let priceDescription: String
    public let iconName: String

    public init(id: String, title: String, priceDescription: String, iconName: String) {
        self.id = id
        self.title = title
        self.priceDescription = priceDescription
        self.iconName = iconName
    }
}

public struct LaundryServiceView: View {
    public let onDismiss: () -> Void

    @State private var options: [LaundryServiceOption] = [
        LaundryServiceOption(id: "lo-1", title: "Wash & Fold", priceDescription: "₹69 / kg • Cleaned & neatly packed", iconName: "tshirt.fill"),
        LaundryServiceOption(id: "lo-2", title: "Wash & Steam Iron", priceDescription: "₹99 / kg • Crisp wrinkle-free finish", iconName: "washer.fill"),
        LaundryServiceOption(id: "lo-3", title: "Premium Dry Clean", priceDescription: "From ₹149 / item • Suits & sarees", iconName: "sparkles")
    ]
    @State private var selectedOptionId: String = "lo-1"
    @State private var isBooking: Bool = false

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
                        // Header Banner
                        HStack(spacing: 12) {
                            ZStack {
                                Circle().fill(UsColors.postbookPrimary.opacity(0.2)).frame(width: 44, height: 44)
                                Image(systemName: "washer.fill")
                                    .foregroundColor(UsColors.postbookPrimary)
                                    .font(.system(size: 20))
                            }

                            VStack(alignment: .leading, spacing: 2) {
                                Text("Doorstep Laundry & Dry Clean 🧺")
                                    .font(.system(size: 15, weight: .bold))
                                    .foregroundColor(UsColors.textPrimary)
                                Text("Pickup in 30 mins • Eco-friendly detergents")
                                    .font(.system(size: 11))
                                    .foregroundColor(UsColors.textMuted)
                            }
                        }
                        .padding(14)
                        .frame(maxWidth: .infinity, alignment: .leading)
                        .background(UsColors.bgSecondary)
                        .clipShape(RoundedRectangle(cornerRadius: 16))

                        Text("Select Service Type")
                            .font(.system(size: 15, weight: .bold))
                            .foregroundColor(UsColors.textPrimary)

                        LazyVStack(spacing: 12) {
                            ForEach(options) { opt in
                                let isSelected = selectedOptionId == opt.id
                                Button(action: {
                                    selectedOptionId = opt.id
                                    HapticManager.shared.trigger(.selection)
                                }) {
                                    HStack(spacing: 14) {
                                        Image(systemName: opt.iconName)
                                            .font(.system(size: 22))
                                            .foregroundColor(isSelected ? .black : UsColors.postbookPrimary)
                                            .frame(width: 36)

                                        VStack(alignment: .leading, spacing: 2) {
                                            Text(opt.title)
                                                .font(.system(size: 14, weight: .bold))
                                                .foregroundColor(isSelected ? .black : UsColors.textPrimary)

                                            Text(opt.priceDescription)
                                                .font(.system(size: 11))
                                                .foregroundColor(isSelected ? Color.black.opacity(0.7) : UsColors.textMuted)
                                        }

                                        Spacer()

                                        if isSelected {
                                            Image(systemName: "checkmark.circle.fill")
                                                .foregroundColor(.black)
                                        }
                                    }
                                    .padding(16)
                                    .background(isSelected ? Color.white : UsColors.bgSecondary)
                                    .clipShape(RoundedRectangle(cornerRadius: 16))
                                }
                                .buttonStyle(.plain)
                            }
                        }

                        Spacer()

                        Button(action: schedulePickup) {
                            HStack {
                                Spacer()
                                if isBooking {
                                    ProgressView().tint(.black)
                                } else {
                                    Text("Schedule Doorstep Pickup")
                                        .font(.system(size: 15, weight: .bold))
                                        .foregroundColor(.black)
                                }
                                Spacer()
                            }
                            .padding(.vertical, 16)
                            .background(Color.white)
                            .clipShape(RoundedRectangle(cornerRadius: 14))
                        }
                        .disabled(isBooking)
                    }
                    .padding(16)
                }
            }
            .navigationTitle("Laundry Service")
            .navigationBarTitleDisplayMode(.inline)
            .toolbar {
                ToolbarItem(placement: .cancellationAction) {
                    Button("Close", action: onDismiss)
                        .foregroundColor(UsColors.textMuted)
                }
            }
        }
    }

    private func schedulePickup() {
        isBooking = true
        DispatchQueue.main.asyncAfter(deadline: .now() + 1.2) {
            isBooking = false
            HapticManager.shared.trigger(.success)
            ToastManager.shared.show("🧺 Laundry pickup rider assigned! Arriving in 25 mins", style: .success)
            onDismiss()
        }
    }
}
