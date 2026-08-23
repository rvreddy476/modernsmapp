import SwiftUI
import UsModel
import UsDesignSystem
import UsNetwork

public struct ParkingSpotItem: Identifiable {
    public let id: String
    public let name: String
    public let location: String
    public let hourlyRate: String
    public let availableSlots: Int
    public let distanceKm: Double

    public init(id: String, name: String, location: String, hourlyRate: String, availableSlots: Int, distanceKm: Double) {
        self.id = id
        self.name = name
        self.location = location
        self.hourlyRate = hourlyRate
        self.availableSlots = availableSlots
        self.distanceKm = distanceKm
    }
}

public struct ParkingBookingView: View {
    public let onDismiss: () -> Void

    @State private var spots: [ParkingSpotItem] = [
        ParkingSpotItem(id: "ps-1", name: "Phoenix Marketcity Basement P2", location: "Whitefield, Bengaluru", hourlyRate: "₹40/hr", availableSlots: 48, distanceKm: 1.4),
        ParkingSpotItem(id: "ps-2", name: "Indiranagar 100ft Road Smart Street Park", location: "Indiranagar, Bengaluru", hourlyRate: "₹30/hr", availableSlots: 8, distanceKm: 2.1),
        ParkingSpotItem(id: "ps-3", name: "Kempegowda Int'l Airport Terminal 2", location: "Devanahalli, Bengaluru", hourlyRate: "₹100/hr", availableSlots: 210, distanceKm: 18.5)
    ]
    @State private var selectedSpot: ParkingSpotItem? = nil
    @State private var bookingDurationHours: Int = 2
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
                    VStack(alignment: .leading, spacing: 18) {
                        Text("Real-Time Parking Spots Nearby")
                            .font(.system(size: 13))
                            .foregroundColor(UsColors.textMuted)

                        LazyVStack(spacing: 12) {
                            ForEach(spots) { spot in
                                parkingSpotCard(spot)
                            }
                        }
                    }
                    .padding(16)
                }
            }
            .navigationTitle("Smart Parking")
            .navigationBarTitleDisplayMode(.inline)
            .toolbar {
                ToolbarItem(placement: .cancellationAction) {
                    Button("Close", action: onDismiss)
                        .foregroundColor(UsColors.textMuted)
                }
            }
            .sheet(item: $selectedSpot) { spot in
                parkingConfirmSheet(spot)
            }
        }
    }

    @ViewBuilder
    private func parkingSpotCard(_ spot: ParkingSpotItem) -> some View {
        Button(action: {
            selectedSpot = spot
            HapticManager.shared.trigger(.selection)
        }) {
            HStack(spacing: 14) {
                ZStack {
                    RoundedRectangle(cornerRadius: 12)
                        .fill(UsColors.postbookPrimary.opacity(0.15))
                        .frame(width: 48, height: 48)

                    Text("P")
                        .font(.system(size: 24, weight: .black, design: .rounded))
                        .foregroundColor(UsColors.postbookPrimary)
                }

                VStack(alignment: .leading, spacing: 2) {
                    Text(spot.name)
                        .font(.system(size: 14, weight: .bold))
                        .foregroundColor(UsColors.textPrimary)
                        .lineLimit(1)

                    Text("\(spot.location) • \(String(format: "%.1f km", spot.distanceKm))")
                        .font(.system(size: 11))
                        .foregroundColor(UsColors.textMuted)

                    Text("\(spot.availableSlots) slots available")
                        .font(.system(size: 11, weight: .bold))
                        .foregroundColor(UsColors.onlineGreen)
                }

                Spacer()

                Text(spot.hourlyRate)
                    .font(.system(size: 14, weight: .bold, design: .rounded))
                    .foregroundColor(UsColors.textPrimary)
            }
            .padding(14)
            .background(UsColors.bgSecondary)
            .clipShape(RoundedRectangle(cornerRadius: 16))
        }
        .buttonStyle(.plain)
    }

    @ViewBuilder
    private func parkingConfirmSheet(_ spot: ParkingSpotItem) -> some View {
        NavigationStack {
            ZStack {
                UsColors.bgPrimary.ignoresSafeArea()

                VStack(spacing: 20) {
                    VStack(spacing: 8) {
                        Text(spot.name)
                            .font(.system(size: 18, weight: .bold))
                            .foregroundColor(UsColors.textPrimary)
                            .multilineTextAlignment(.center)
                        Text(spot.location)
                            .font(.system(size: 13))
                            .foregroundColor(UsColors.textMuted)
                    }
                    .padding(.top, 12)

                    // Hours selector
                    HStack(spacing: 10) {
                        ForEach([1, 2, 4, 8], id: \.self) { hrs in
                            let isSelected = bookingDurationHours == hrs
                            Button(action: { bookingDurationHours = hrs }) {
                                Text("\(hrs) \(hrs == 1 ? "Hour" : "Hours")")
                                    .font(.system(size: 13, weight: .bold))
                                    .foregroundColor(isSelected ? .black : UsColors.textPrimary)
                                    .padding(.horizontal, 14)
                                    .padding(.vertical, 8)
                                    .background(isSelected ? Color.white : UsColors.bgSecondary)
                                    .clipShape(Capsule())
                            }
                            .buttonStyle(.plain)
                        }
                    }

                    Spacer()

                    // Book with FASTag / UPI Button
                    Button(action: {
                        isBooking = true
                        DispatchQueue.main.asyncAfter(deadline: .now() + 1.0) {
                            isBooking = false
                            selectedSpot = nil
                            HapticManager.shared.trigger(.success)
                            ToastManager.shared.show("🅿️ Parking Slot Reserved at \(spot.name)!", style: .success)
                        }
                    }) {
                        HStack {
                            Spacer()
                            if isBooking {
                                ProgressView().tint(.black)
                            } else {
                                Text("Book Slot via FASTag / UPI")
                                    .font(.system(size: 15, weight: .bold))
                                    .foregroundColor(.black)
                            }
                            Spacer()
                        }
                        .padding(.vertical, 16)
                        .background(Color.white)
                        .clipShape(RoundedRectangle(cornerRadius: 14))
                    }
                    .padding(16)
                }
                .padding(16)
            }
            .navigationTitle("Confirm Parking")
            .navigationBarTitleDisplayMode(.inline)
            .toolbar {
                ToolbarItem(placement: .cancellationAction) {
                    Button("Cancel") { selectedSpot = nil }
                        .foregroundColor(UsColors.textMuted)
                }
            }
        }
    }
}
