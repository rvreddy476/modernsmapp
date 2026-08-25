import SwiftUI
import UsModel
import UsDesignSystem
import UsNetwork

public struct EVStationItem: Identifiable {
    public let id: String
    public let name: String
    public let network: String
    public let speedKW: String
    public let connectorTypes: [String]
    public let availableChargers: Int
    public let totalChargers: Int
    public let distanceKm: Double

    public init(id: String, name: String, network: String, speedKW: String, connectorTypes: [String], availableChargers: Int, totalChargers: Int, distanceKm: Double) {
        self.id = id
        self.name = name
        self.network = network
        self.speedKW = speedKW
        self.connectorTypes = connectorTypes
        self.availableChargers = availableChargers
        self.totalChargers = totalChargers
        self.distanceKm = distanceKm
    }
}

public struct EVChargingView: View {
    public let onDismiss: () -> Void

    @State private var stations: [EVStationItem] = [
        EVStationItem(id: "ev-1", name: "Tata Power EZ Charge - Indiranagar", network: "Tata Power", speedKW: "60 kW DC Fast", connectorTypes: ["CCS2", "Type 2"], availableChargers: 3, totalChargers: 4, distanceKm: 1.2),
        EVStationItem(id: "ev-2", name: "Ather Grid Supercharger - Koramangala", network: "Ather Grid", speedKW: "Fast DC", connectorTypes: ["Ather Dot", "Type 2"], availableChargers: 4, totalChargers: 6, distanceKm: 2.4),
        EVStationItem(id: "ev-3", name: "Jio-bp pulse Fast Hub - MG Road", network: "Jio-bp", speedKW: "120 kW Dual Gun", connectorTypes: ["CCS2", "CHAdeMO"], availableChargers: 2, totalChargers: 4, distanceKm: 4.8)
    ]
    @State private var selectedStation: EVStationItem? = nil

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
                        // Hero Status
                        HStack(spacing: 12) {
                            ZStack {
                                Circle().fill(UsColors.onlineGreen.opacity(0.2)).frame(width: 44, height: 44)
                                Image(systemName: "bolt.car.fill")
                                    .foregroundColor(UsColors.onlineGreen)
                                    .font(.system(size: 20))
                            }

                            VStack(alignment: .leading, spacing: 2) {
                                Text("Find & Book EV Fast Chargers ⚡️")
                                    .font(.system(size: 15, weight: .bold))
                                    .foregroundColor(UsColors.textPrimary)
                                Text("Real-time gun availability • Seamless UPI auto-pay")
                                    .font(.system(size: 11))
                                    .foregroundColor(UsColors.textMuted)
                            }
                        }
                        .padding(14)
                        .frame(maxWidth: .infinity, alignment: .leading)
                        .background(UsColors.bgSecondary)
                        .clipShape(RoundedRectangle(cornerRadius: 16))

                        Text("Nearby EV Charging Hubs")
                            .font(.system(size: 15, weight: .bold))
                            .foregroundColor(UsColors.textPrimary)

                        LazyVStack(spacing: 12) {
                            ForEach(stations) { station in
                                stationCard(station)
                            }
                        }
                    }
                    .padding(16)
                }
            }
            .navigationTitle("EV Charging")
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
    private func stationCard(_ station: EVStationItem) -> some View {
        Button(action: {
            HapticManager.shared.trigger(.success)
            ToastManager.shared.show("⚡️ Reserved 1x \(station.speedKW) Gun at \(station.name)!", style: .success)
        }) {
            VStack(alignment: .leading, spacing: 10) {
                HStack {
                    VStack(alignment: .leading, spacing: 2) {
                        Text(station.network)
                            .font(.system(size: 11, weight: .bold))
                            .foregroundColor(UsColors.postbookPrimary)

                        Text(station.name)
                            .font(.system(size: 14, weight: .bold))
                            .foregroundColor(UsColors.textPrimary)
                            .lineLimit(1)
                    }

                    Spacer()

                    Text("\(station.availableChargers)/\(station.totalChargers) Free")
                        .font(.system(size: 12, weight: .bold))
                        .foregroundColor(UsColors.onlineGreen)
                        .padding(.horizontal, 8)
                        .padding(.vertical, 4)
                        .background(UsColors.onlineGreen.opacity(0.15))
                        .clipShape(Capsule())
                }

                HStack(spacing: 8) {
                    Text(station.speedKW)
                        .font(.system(size: 11, weight: .semibold))
                        .foregroundColor(UsColors.textPrimary)
                        .padding(.horizontal, 8)
                        .padding(.vertical, 3)
                        .background(UsColors.bgTertiary)
                        .clipShape(Capsule())

                    ForEach(station.connectorTypes, id: \.self) { conn in
                        Text(conn)
                            .font(.system(size: 11))
                            .foregroundColor(UsColors.textMuted)
                    }

                    Spacer()

                    Text(String(format: "%.1f km away", station.distanceKm))
                        .font(.system(size: 11))
                        .foregroundColor(UsColors.textMuted)
                }
            }
            .padding(14)
            .background(UsColors.bgSecondary)
            .clipShape(RoundedRectangle(cornerRadius: 16))
        }
        .buttonStyle(.plain)
    }
}
