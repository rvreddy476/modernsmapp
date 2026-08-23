import SwiftUI
import UsModel
import UsDesignSystem
import UsNetwork

public struct GymCenterItem: Identifiable {
    public let id: String
    public let name: String
    public let location: String
    public let dayPassPrice: String
    public let facilities: [String]
    public let distanceKm: Double

    public init(id: String, name: String, location: String, dayPassPrice: String, facilities: [String], distanceKm: Double) {
        self.id = id
        self.name = name
        self.location = location
        self.dayPassPrice = dayPassPrice
        self.facilities = facilities
        self.distanceKm = distanceKm
    }
}

public struct GymPassBookingView: View {
    public let onDismiss: () -> Void

    @State private var gyms: [GymCenterItem] = [
        GymCenterItem(id: "gym-1", name: "Cult.fit Center - Indiranagar", location: "100ft Road, Bengaluru", dayPassPrice: "₹299", facilities: ["HRX Strength", "Boxing", "Steam"], distanceKm: 1.1),
        GymCenterItem(id: "gym-2", name: "Gold's Gym Elite - Koramangala", location: "80ft Road, Bengaluru", dayPassPrice: "₹349", facilities: ["Heavy Weights", "Cardio Zone", "Sauna"], distanceKm: 2.8),
        GymCenterItem(id: "gym-3", name: "CrossFit Box 560038", location: "HAL 2nd Stage, Bengaluru", dayPassPrice: "₹399", facilities: ["Olympic Lifting", "HIIT", "Rowers"], distanceKm: 3.4)
    ]
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
                        // Hero Banner
                        HStack(spacing: 12) {
                            ZStack {
                                Circle().fill(UsColors.onlineGreen.opacity(0.2)).frame(width: 44, height: 44)
                                Image(systemName: "figure.run")
                                    .foregroundColor(UsColors.onlineGreen)
                                    .font(.system(size: 22))
                            }

                            VStack(alignment: .leading, spacing: 2) {
                                Text("1-Day Flexi Gym & Fitness Passes 🏋️")
                                    .font(.system(size: 15, weight: .bold))
                                    .foregroundColor(UsColors.textPrimary)
                                Text("No monthly commitments • Instant QR entry pass")
                                    .font(.system(size: 11))
                                    .foregroundColor(UsColors.textMuted)
                            }
                        }
                        .padding(14)
                        .frame(maxWidth: .infinity, alignment: .leading)
                        .background(UsColors.bgSecondary)
                        .clipShape(RoundedRectangle(cornerRadius: 16))

                        Text("Nearby Fitness Centers")
                            .font(.system(size: 15, weight: .bold))
                            .foregroundColor(UsColors.textPrimary)

                        LazyVStack(spacing: 12) {
                            ForEach(gyms) { gym in
                                gymCard(gym)
                            }
                        }
                    }
                    .padding(16)
                }
            }
            .navigationTitle("Fitness & Gym Passes")
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
    private func gymCard(_ gym: GymCenterItem) -> some View {
        VStack(alignment: .leading, spacing: 10) {
            HStack {
                VStack(alignment: .leading, spacing: 2) {
                    Text(gym.name)
                        .font(.system(size: 14, weight: .bold))
                        .foregroundColor(UsColors.textPrimary)
                    Text("\(gym.location) • \(String(format: "%.1f km", gym.distanceKm))")
                        .font(.system(size: 11))
                        .foregroundColor(UsColors.textMuted)
                }

                Spacer()

                Text(gym.dayPassPrice)
                    .font(.system(size: 16, weight: .black, design: .rounded))
                    .foregroundColor(UsColors.onlineGreen)
            }

            HStack(spacing: 6) {
                ForEach(gym.facilities, id: \.self) { fac in
                    Text(fac)
                        .font(.system(size: 10, weight: .semibold))
                        .foregroundColor(UsColors.textPrimary)
                        .padding(.horizontal, 8)
                        .padding(.vertical, 3)
                        .background(UsColors.bgTertiary)
                        .clipShape(Capsule())
                }
            }

            Divider().background(UsColors.borderSubtle)

            Button(action: {
                HapticManager.shared.trigger(.success)
                ToastManager.shared.show("🎉 Booked 1-Day Pass at \(gym.name)! QR pass active.", style: .success)
            }) {
                HStack {
                    Spacer()
                    Text("Book Pass for \(gym.dayPassPrice)")
                        .font(.system(size: 13, weight: .bold))
                        .foregroundColor(.black)
                    Spacer()
                }
                .padding(.vertical, 10)
                .background(Color.white)
                .clipShape(RoundedRectangle(cornerRadius: 10))
            }
        }
        .padding(14)
        .background(UsColors.bgSecondary)
        .clipShape(RoundedRectangle(cornerRadius: 16))
    }
}
