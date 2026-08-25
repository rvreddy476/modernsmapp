import SwiftUI
import UsModel
import UsDesignSystem
import UsNetwork

public struct RideEstimateRequest: Codable, Sendable {
    public let pickup: String
    public let drop: String
    public let vehicleType: String

    public init(pickup: String, drop: String, vehicleType: String) {
        self.pickup = pickup
        self.drop = drop
        self.vehicleType = vehicleType
    }
}

public struct RideEstimateResponse: Codable, Sendable {
    public let fareEstimatePaise: Int64
    public let formattedFare: String
    public let etaMins: Int
}

@Observable
public final class RideBookingViewModel: @unchecked Sendable {
    public var pickupLocation: String = "Current Location (Koramangala 4th Block)"
    public var dropLocation: String = "Indiranagar 100ft Road"
    public var selectedVehicle: RideVehicleType = .auto
    public var isBooking: Bool = false
    public var isDriverMatched: Bool = false

    private let client: APIClientProtocol

    public init(client: APIClientProtocol = APIClient()) {
        self.client = client
    }

    public func calculateFare(for type: RideVehicleType) -> String {
        let baseFare = 95.0
        let total = baseFare * type.priceMultiplier
        return String(format: "₹%.0f", total)
    }

    @MainActor
    public func bookRide(onMatched: @escaping () -> Void) {
        isBooking = true

        Task {
            // Attempt live backend dispatch to rider-service
            let payload = RideEstimateRequest(
                pickup: pickupLocation,
                drop: dropLocation,
                vehicleType: selectedVehicle.rawValue
            )
            let data = try? JSONEncoder().encode(payload)
            let _: [String: String]? = try? await client.request(
                endpoint: "v1/rider/rides",
                method: "POST",
                query: nil,
                body: data
            )

            // Simulate realistic matching delay
            try? await Task.sleep(nanoseconds: 1_500_000_000)
            await MainActor.run {
                self.isBooking = false
                self.isDriverMatched = true
                onMatched()
            }
        }
    }
}

public struct RideBookingView: View {
    @State private var viewModel: RideBookingViewModel
    public let onDismiss: () -> Void

    public init(client: APIClientProtocol = APIClient(), onDismiss: @escaping () -> Void = {}) {
        _viewModel = State(initialValue: RideBookingViewModel(client: client))
        self.onDismiss = onDismiss
    }

    public var body: some View {
        NavigationStack {
            ZStack {
                UsColors.bgPrimary
                    .ignoresSafeArea()

                VStack(spacing: 0) {
                    // Map Placeholder Simulation
                    ZStack {
                        Color(red: 0x14/255.0, green: 0x1E/255.0, blue: 0x28/255.0)

                        VStack {
                            Image(systemName: "map.fill")
                                .font(.system(size: 40))
                                .foregroundColor(UsColors.postbookPrimary.opacity(0.4))
                            Text("Live Route Simulation")
                                .font(.system(size: 13, weight: .semibold))
                                .foregroundColor(UsColors.textMuted)
                        }
                    }
                    .frame(height: 200)

                    // Booking Panel
                    ScrollView {
                        VStack(spacing: 16) {
                            // Location inputs
                            VStack(spacing: 10) {
                                HStack(spacing: 10) {
                                    Circle().fill(UsColors.onlineGreen).frame(width: 8, height: 8)
                                    TextField("Pickup Location", text: $viewModel.pickupLocation)
                                        .font(.system(size: 14))
                                        .foregroundColor(UsColors.textPrimary)
                                }
                                .padding(12)
                                .background(UsColors.bgSecondary)
                                .clipShape(RoundedRectangle(cornerRadius: 10))

                                HStack(spacing: 10) {
                                    Circle().fill(UsColors.postgramPrimary).frame(width: 8, height: 8)
                                    TextField("Where to?", text: $viewModel.dropLocation)
                                        .font(.system(size: 14))
                                        .foregroundColor(UsColors.textPrimary)
                                }
                                .padding(12)
                                .background(UsColors.bgSecondary)
                                .clipShape(RoundedRectangle(cornerRadius: 10))
                            }

                            // Vehicle Options
                            Text("Choose a Ride")
                                .font(.system(size: 16, weight: .bold))
                                .foregroundColor(UsColors.textPrimary)
                                .frame(maxWidth: .infinity, alignment: .leading)

                            VStack(spacing: 10) {
                                ForEach(RideVehicleType.allCases) { vehicle in
                                    vehicleOptionRow(vehicle)
                                }
                            }

                            // Confirm Button
                            Button(action: {
                                viewModel.bookRide {
                                    ToastManager.shared.show("Driver Found! Ramesh (KA 01 AH 4821) is arriving in 3 mins", style: .success)
                                    onDismiss()
                                }
                            }) {
                                HStack {
                                    Spacer()
                                    if viewModel.isBooking {
                                        ProgressView().tint(.black)
                                        Text("Finding Nearby Driver...")
                                            .font(.system(size: 15, weight: .bold))
                                            .foregroundColor(.black)
                                            .padding(.leading, 8)
                                    } else {
                                        Text("Book \(viewModel.selectedVehicle.rawValue) (\(viewModel.calculateFare(for: viewModel.selectedVehicle)))")
                                            .font(.system(size: 16, weight: .bold))
                                            .foregroundColor(.black)
                                    }
                                    Spacer()
                                }
                                .padding(.vertical, 16)
                                .background(Color.white)
                                .clipShape(RoundedRectangle(cornerRadius: 14))
                            }
                            .disabled(viewModel.isBooking)
                            .padding(.top, 8)
                        }
                        .padding(16)
                    }
                }
            }
            .navigationTitle("US Rides")
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
    private func vehicleOptionRow(_ vehicle: RideVehicleType) -> some View {
        let isSelected = viewModel.selectedVehicle == vehicle
        Button(action: { viewModel.selectedVehicle = vehicle }) {
            HStack(spacing: 14) {
                ZStack {
                    RoundedRectangle(cornerRadius: 10)
                        .fill(isSelected ? UsColors.postbookPrimary.opacity(0.2) : UsColors.bgTertiary)
                        .frame(width: 48, height: 48)

                    Image(systemName: vehicle.icon)
                        .font(.system(size: 20))
                        .foregroundColor(isSelected ? UsColors.postbookPrimary : UsColors.textPrimary)
                }

                VStack(alignment: .leading, spacing: 2) {
                    Text(vehicle.rawValue)
                        .font(.system(size: 15, weight: .bold))
                        .foregroundColor(UsColors.textPrimary)

                    Text("\(vehicle.etaMins) mins away • Drop 12:45 PM")
                        .font(.system(size: 12))
                        .foregroundColor(UsColors.textMuted)
                }

                Spacer()

                Text(viewModel.calculateFare(for: vehicle))
                    .font(.system(size: 16, weight: .bold, design: .rounded))
                    .foregroundColor(UsColors.textPrimary)
            }
            .padding(12)
            .background(isSelected ? UsColors.bgSecondary : UsColors.bgSecondary.opacity(0.5))
            .clipShape(RoundedRectangle(cornerRadius: 14))
            .overlay(
                RoundedRectangle(cornerRadius: 14)
                    .stroke(isSelected ? UsColors.postbookPrimary : UsColors.borderSubtle, lineWidth: isSelected ? 1.5 : 1)
            )
        }
        .buttonStyle(.plain)
    }
}
