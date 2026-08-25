import SwiftUI
import UsModel
import UsDesignSystem
import UsNetwork

public struct CarpoolRideItem: Identifiable {
    public let id: String
    public let driverName: String
    public let company: String
    public let route: String
    public let departureTime: String
    public let seatsAvailable: Int
    public let pricePerSeat: String

    public init(id: String, driverName: String, company: String, route: String, departureTime: String, seatsAvailable: Int, pricePerSeat: String) {
        self.id = id
        self.driverName = driverName
        self.company = company
        self.route = route
        self.departureTime = departureTime
        self.seatsAvailable = seatsAvailable
        self.pricePerSeat = pricePerSeat
    }
}

public struct CarpoolView: View {
    @State private var viewModel: CarpoolViewModel
    public let onDismiss: () -> Void

    public init(
        client: APIClientProtocol = APIClient(),
        onDismiss: @escaping () -> Void = {}
    ) {
        _viewModel = State(initialValue: CarpoolViewModel(client: client))
        self.onDismiss = onDismiss
    }

    public var body: some View {
        NavigationStack {
            ZStack {
                UsColors.bgPrimary
                    .ignoresSafeArea()

                ScrollView {
                    VStack(alignment: .leading, spacing: 18) {
                        // Banner
                        HStack(spacing: 12) {
                            ZStack {
                                Circle().fill(UsColors.onlineGreen.opacity(0.2)).frame(width: 44, height: 44)
                                Image(systemName: "car.2.fill")
                                    .foregroundColor(UsColors.onlineGreen)
                                    .font(.system(size: 20))
                            }

                            VStack(alignment: .leading, spacing: 2) {
                                Text("Tech Park Carpool & Ride Share 🚗")
                                    .font(.system(size: 15, weight: .bold))
                                    .foregroundColor(UsColors.textPrimary)
                                Text("Verified corporate coworkers • Split fuel costs")
                                    .font(.system(size: 11))
                                    .foregroundColor(UsColors.textMuted)
                            }
                        }
                        .padding(14)
                        .frame(maxWidth: .infinity, alignment: .leading)
                        .background(UsColors.bgSecondary)
                        .clipShape(RoundedRectangle(cornerRadius: 16))

                        Text("Scheduled Rides Today")
                            .font(.system(size: 15, weight: .bold))
                            .foregroundColor(UsColors.textPrimary)

                        if viewModel.isLoading {
                            ProgressView()
                                .tint(UsColors.postbookPrimary)
                                .frame(maxWidth: .infinity, alignment: .center)
                                .padding(.vertical, 20)
                        } else {
                            LazyVStack(spacing: 12) {
                                ForEach(viewModel.rides) { ride in
                                    carpoolCard(ride)
                                }
                            }
                        }
                    }
                    .padding(16)
                }
            }
            .navigationTitle("Carpool")
            .navigationBarTitleDisplayMode(.inline)
            .toolbar {
                ToolbarItem(placement: .cancellationAction) {
                    Button("Close", action: onDismiss)
                        .foregroundColor(UsColors.textMuted)
                }
            }
            .task {
                await viewModel.fetchRides()
            }
        }
    }

    @ViewBuilder
    private func carpoolCard(_ ride: CarpoolRideItem) -> some View {
        VStack(alignment: .leading, spacing: 10) {
            HStack {
                UsAvatar(name: ride.driverName, size: .small)

                VStack(alignment: .leading, spacing: 2) {
                    HStack(spacing: 4) {
                        Text(ride.driverName)
                            .font(.system(size: 13, weight: .bold))
                            .foregroundColor(UsColors.textPrimary)

                        Image(systemName: "checkmark.seal.fill")
                            .foregroundColor(UsColors.postbookPrimary)
                            .font(.system(size: 10))
                    }

                    Text("🏢 \(ride.company)")
                        .font(.system(size: 10))
                        .foregroundColor(UsColors.textMuted)
                }

                Spacer()

                VStack(alignment: .trailing, spacing: 2) {
                    Text(ride.pricePerSeat)
                        .font(.system(size: 15, weight: .black, design: .rounded))
                        .foregroundColor(UsColors.onlineGreen)
                    Text("\(ride.seatsAvailable) seats left")
                        .font(.system(size: 9))
                        .foregroundColor(UsColors.textMuted)
                }
            }

            Divider().background(UsColors.borderSubtle)

            HStack {
                VStack(alignment: .leading, spacing: 2) {
                    Text("📍 \(ride.route)")
                        .font(.system(size: 12, weight: .semibold))
                        .foregroundColor(UsColors.textPrimary)
                    Text("⏰ Leaves at \(ride.departureTime)")
                        .font(.system(size: 10))
                        .foregroundColor(UsColors.textMuted)
                }

                Spacer()

                Button(action: {
                    Task {
                        _ = try? await viewModel.joinRide(rideId: ride.id)
                        HapticManager.shared.trigger(.success)
                        ToastManager.shared.show("Booked seat with \(ride.driverName)!", style: .success)
                    }
                }) {
                    Text("Join Carpool")
                        .font(.system(size: 12, weight: .bold))
                        .foregroundColor(.black)
                        .padding(.horizontal, 14)
                        .padding(.vertical, 6)
                        .background(Color.white)
                        .clipShape(Capsule())
                }
            }
        }
        .padding(14)
        .background(UsColors.bgSecondary)
        .clipShape(RoundedRectangle(cornerRadius: 14))
    }
}
