import SwiftUI
import UsModel
import UsDesignSystem
import UsNetwork

public struct TransitBookingStatus: Identifiable {
    public let id: String
    public let pnr: String
    public let title: String
    public let transitType: String
    public let statusText: String
    public let platformOrGate: String
    public let departureTime: String

    public init(id: String, pnr: String, title: String, transitType: String, statusText: String, platformOrGate: String, departureTime: String) {
        self.id = id
        self.pnr = pnr
        self.title = title
        self.transitType = transitType
        self.statusText = statusText
        self.platformOrGate = platformOrGate
        self.departureTime = departureTime
    }
}

public struct PNRTrackerView: View {
    @State private var viewModel: PNRTrackerViewModel
    @State private var inputPNR: String = ""
    public let onDismiss: () -> Void

    public init(
        client: APIClientProtocol = APIClient(),
        onDismiss: @escaping () -> Void = {}
    ) {
        _viewModel = State(initialValue: PNRTrackerViewModel(client: client))
        self.onDismiss = onDismiss
    }

    public var body: some View {
        NavigationStack {
            ZStack {
                UsColors.bgPrimary
                    .ignoresSafeArea()

                ScrollView {
                    VStack(alignment: .leading, spacing: 20) {
                        // Search Box
                        VStack(alignment: .leading, spacing: 8) {
                            Text("Track IRCTC Train & Flight PNR")
                                .font(.system(size: 13, weight: .bold))
                                .foregroundColor(UsColors.textPrimary)

                            HStack {
                                Image(systemName: "magnifyingglass")
                                    .foregroundColor(UsColors.textMuted)

                                TextField("Enter 10-digit PNR / Flight Code", text: $inputPNR)
                                    .foregroundColor(.white)

                                Button(action: {
                                    guard !inputPNR.isEmpty else { return }
                                    Task {
                                        _ = try? await viewModel.trackPNR(pnr: inputPNR)
                                        HapticManager.shared.trigger(.success)
                                        ToastManager.shared.show("PNR \(inputPNR) tracking activated!", style: .success)
                                        inputPNR = ""
                                    }
                                }) {
                                    Text("Track")
                                        .font(.system(size: 12, weight: .bold))
                                        .foregroundColor(.black)
                                        .padding(.horizontal, 14)
                                        .padding(.vertical, 6)
                                        .background(Color.white)
                                        .clipShape(Capsule())
                                }
                            }
                            .padding(12)
                            .background(UsColors.bgSecondary)
                            .clipShape(RoundedRectangle(cornerRadius: 12))
                        }

                        Text("Active Trips & Live Status")
                            .font(.system(size: 15, weight: .bold))
                            .foregroundColor(UsColors.textPrimary)

                        if viewModel.isLoading {
                            ProgressView()
                                .tint(UsColors.postbookPrimary)
                                .frame(maxWidth: .infinity, alignment: .center)
                                .padding(.vertical, 20)
                        } else {
                            LazyVStack(spacing: 12) {
                                ForEach(viewModel.trackedTrips) { trip in
                                    tripCard(trip)
                                }
                            }
                        }
                    }
                    .padding(16)
                }
            }
            .navigationTitle("Live PNR Tracker")
            .navigationBarTitleDisplayMode(.inline)
            .toolbar {
                ToolbarItem(placement: .cancellationAction) {
                    Button("Close", action: onDismiss)
                        .foregroundColor(UsColors.textMuted)
                }
            }
            .task {
                await viewModel.fetchTrackedTrips()
            }
        }
    }

    @ViewBuilder
    private func tripCard(_ trip: TransitBookingStatus) -> some View {
        VStack(alignment: .leading, spacing: 10) {
            HStack {
                VStack(alignment: .leading, spacing: 2) {
                    Text(trip.title)
                        .font(.system(size: 14, weight: .bold))
                        .foregroundColor(UsColors.textPrimary)
                    Text("PNR: \(trip.pnr) • \(trip.transitType)")
                        .font(.system(size: 11))
                        .foregroundColor(UsColors.textMuted)
                }

                Spacer()

                Text(trip.statusText)
                    .font(.system(size: 11, weight: .bold))
                    .foregroundColor(UsColors.onlineGreen)
                    .padding(.horizontal, 8)
                    .padding(.vertical, 4)
                    .background(UsColors.onlineGreen.opacity(0.15))
                    .clipShape(Capsule())
            }

            Divider().background(UsColors.borderSubtle)

            HStack {
                Text("📍 \(trip.platformOrGate)")
                    .font(.system(size: 12, weight: .semibold))
                    .foregroundColor(UsColors.textPrimary)

                Spacer()

                Text("⏰ Departure: \(trip.departureTime)")
                    .font(.system(size: 11, weight: .monospaced))
                    .foregroundColor(UsColors.postbookPrimary)
            }
        }
        .padding(14)
        .background(UsColors.bgSecondary)
        .clipShape(RoundedRectangle(cornerRadius: 14))
    }
}
