import SwiftUI
import UsModel
import UsDesignSystem
import UsNetwork

public struct TurfArenaItem: Identifiable {
    public let id: String
    public let name: String
    public let sport: String
    public let area: String
    public let pricePerHour: String
    public let rating: Double

    public init(id: String, name: String, sport: String, area: String, pricePerHour: String, rating: Double) {
        self.id = id
        self.name = name
        self.sport = sport
        self.area = area
        self.pricePerHour = pricePerHour
        self.rating = rating
    }
}

public struct TurfBookingView: View {
    @State private var viewModel: TurfBookingViewModel
    public let onDismiss: () -> Void

    public init(
        client: APIClientProtocol = APIClient(),
        onDismiss: @escaping () -> Void = {}
    ) {
        _viewModel = State(initialValue: TurfBookingViewModel(client: client))
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
                                Image(systemName: "sportscourt.fill")
                                    .foregroundColor(UsColors.onlineGreen)
                                    .font(.system(size: 20))
                            }

                            VStack(alignment: .leading, spacing: 2) {
                                Text("Turf & Sports Court Booking ⚽️")
                                    .font(.system(size: 15, weight: .bold))
                                    .foregroundColor(UsColors.textPrimary)
                                Text("Box Cricket, Badminton & Football slots nearby")
                                    .font(.system(size: 11))
                                    .foregroundColor(UsColors.textMuted)
                            }
                        }
                        .padding(14)
                        .frame(maxWidth: .infinity, alignment: .leading)
                        .background(UsColors.bgSecondary)
                        .clipShape(RoundedRectangle(cornerRadius: 16))

                        Text("Available Arenas Near You")
                            .font(.system(size: 15, weight: .bold))
                            .foregroundColor(UsColors.textPrimary)

                        if viewModel.isLoading {
                            ProgressView()
                                .tint(UsColors.postbookPrimary)
                                .frame(maxWidth: .infinity, alignment: .center)
                                .padding(.vertical, 20)
                        } else {
                            LazyVStack(spacing: 12) {
                                ForEach(viewModel.arenas) { arena in
                                    turfCard(arena)
                                }
                            }
                        }
                    }
                    .padding(16)
                }
            }
            .navigationTitle("Sports & Turfs")
            .navigationBarTitleDisplayMode(.inline)
            .toolbar {
                ToolbarItem(placement: .cancellationAction) {
                    Button("Close", action: onDismiss)
                        .foregroundColor(UsColors.textMuted)
                }
            }
            .task {
                await viewModel.fetchArenas()
            }
        }
    }

    @ViewBuilder
    private func turfCard(_ arena: TurfArenaItem) -> some View {
        VStack(alignment: .leading, spacing: 10) {
            HStack {
                VStack(alignment: .leading, spacing: 2) {
                    Text(arena.name)
                        .font(.system(size: 14, weight: .bold))
                        .foregroundColor(UsColors.textPrimary)

                    Text("\(arena.sport) • \(arena.area)")
                        .font(.system(size: 11))
                        .foregroundColor(UsColors.textMuted)
                }

                Spacer()

                VStack(alignment: .trailing, spacing: 2) {
                    Text(arena.pricePerHour)
                        .font(.system(size: 14, weight: .bold, design: .rounded))
                        .foregroundColor(UsColors.onlineGreen)
                    Text("⭐️ \(String(format: "%.1f", arena.rating))")
                        .font(.system(size: 10, weight: .semibold))
                        .foregroundColor(Color.yellow)
                }
            }

            Divider().background(UsColors.borderSubtle)

            HStack {
                Button(action: {
                    HapticManager.shared.trigger(.selection)
                    ToastManager.shared.show("Invited teammates to split turf fee!", style: .info)
                }) {
                    Text("👥 Split with Team")
                        .font(.system(size: 11, weight: .semibold))
                        .foregroundColor(UsColors.textPrimary)
                        .padding(.horizontal, 10)
                        .padding(.vertical, 6)
                        .background(UsColors.bgTertiary)
                        .clipShape(Capsule())
                }

                Spacer()

                Button(action: {
                    Task {
                        let bookingId = try? await viewModel.bookSlot(turfId: arena.id, slotTime: "7:00 PM")
                        HapticManager.shared.trigger(.success)
                        ToastManager.shared.show("Booked slot at \(arena.name)! ID: \(bookingId ?? "TURF-OK") ⚽️", style: .success)
                    }
                }) {
                    Text("Book Slot")
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
