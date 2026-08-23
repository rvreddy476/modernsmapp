import SwiftUI
import UsModel
import UsDesignSystem
import UsNetwork

public struct CoworkingSpaceItem: Identifiable {
    public let id: String
    public let name: String
    public let area: String
    public let dayPassPrice: String
    public let rating: Double
    public let amenities: String

    public init(id: String, name: String, area: String, dayPassPrice: String, rating: Double, amenities: String) {
        self.id = id
        self.name = name
        self.area = area
        self.dayPassPrice = dayPassPrice
        self.rating = rating
        self.amenities = amenities
    }
}

public struct CoworkingBookingView: View {
    @State private var viewModel: CoworkingViewModel
    public let onDismiss: () -> Void

    public init(
        client: APIClientProtocol = APIClient(),
        onDismiss: @escaping () -> Void = {}
    ) {
        _viewModel = State(initialValue: CoworkingViewModel(client: client))
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
                                Circle().fill(UsColors.postbookPrimary.opacity(0.2)).frame(width: 44, height: 44)
                                Image(systemName: "laptopcomputer.and.ipad")
                                    .foregroundColor(UsColors.postbookPrimary)
                                    .font(.system(size: 20))
                            }

                            VStack(alignment: .leading, spacing: 2) {
                                Text("Co-Working Day Pass & Hotdesks 💻")
                                    .font(.system(size: 15, weight: .bold))
                                    .foregroundColor(UsColors.textPrimary)
                                Text("Book premium work desks with instant WiFi credentials")
                                    .font(.system(size: 11))
                                    .foregroundColor(UsColors.textMuted)
                            }
                        }
                        .padding(14)
                        .frame(maxWidth: .infinity, alignment: .leading)
                        .background(UsColors.bgSecondary)
                        .clipShape(RoundedRectangle(cornerRadius: 16))

                        Text("Popular Spaces Near You")
                            .font(.system(size: 15, weight: .bold))
                            .foregroundColor(UsColors.textPrimary)

                        if viewModel.isLoading {
                            ProgressView()
                                .tint(UsColors.postbookPrimary)
                                .frame(maxWidth: .infinity, alignment: .center)
                                .padding(.vertical, 20)
                        } else {
                            LazyVStack(spacing: 12) {
                                ForEach(viewModel.spaces) { space in
                                    spaceCard(space)
                                }
                            }
                        }
                    }
                    .padding(16)
                }
            }
            .navigationTitle("Co-Working")
            .navigationBarTitleDisplayMode(.inline)
            .toolbar {
                ToolbarItem(placement: .cancellationAction) {
                    Button("Close", action: onDismiss)
                        .foregroundColor(UsColors.textMuted)
                }
            }
            .task {
                await viewModel.fetchSpaces()
            }
        }
    }

    @ViewBuilder
    private func spaceCard(_ space: CoworkingSpaceItem) -> some View {
        VStack(alignment: .leading, spacing: 10) {
            HStack {
                VStack(alignment: .leading, spacing: 2) {
                    Text(space.name)
                        .font(.system(size: 15, weight: .bold))
                        .foregroundColor(UsColors.textPrimary)

                    Text(space.area)
                        .font(.system(size: 11))
                        .foregroundColor(UsColors.textMuted)
                }

                Spacer()

                VStack(alignment: .trailing, spacing: 2) {
                    Text(space.dayPassPrice)
                        .font(.system(size: 15, weight: .black, design: .rounded))
                        .foregroundColor(UsColors.onlineGreen)
                    Text("⭐️ \(String(format: "%.1f", space.rating))")
                        .font(.system(size: 10, weight: .semibold))
                        .foregroundColor(Color.yellow)
                }
            }

            Text("✨ \(space.amenities)")
                .font(.system(size: 11))
                .foregroundColor(UsColors.postbookPrimary)

            Divider().background(UsColors.borderSubtle)

            HStack {
                Text("Instant QR Check-in")
                    .font(.system(size: 11))
                    .foregroundColor(UsColors.textMuted)

                Spacer()

                Button(action: {
                    Task {
                        let passId = try? await viewModel.bookDayPass(spaceId: space.id)
                        HapticManager.shared.trigger(.success)
                        ToastManager.shared.show("Booked Pass (\(passId ?? "PASS")): WiFi US-GUEST", style: .success)
                    }
                }) {
                    Text("Book Pass")
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
