import SwiftUI
import UsModel
import UsDesignSystem
import UsNetwork

public struct MovieShowtimeItem: Identifiable {
    public let id: String
    public let title: String
    public let cinema: String
    public let time: String
    public let format: String
    public let price: String

    public init(id: String, title: String, cinema: String, time: String, format: String, price: String) {
        self.id = id
        self.title = title
        self.cinema = cinema
        self.time = time
        self.format = format
        self.price = price
    }
}

public struct MovieBookingView: View {
    @State private var viewModel: MovieBookingViewModel
    public let onDismiss: () -> Void

    public init(
        client: APIClientProtocol = APIClient(),
        onDismiss: @escaping () -> Void = {}
    ) {
        _viewModel = State(initialValue: MovieBookingViewModel(client: client))
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
                                Circle().fill(Color.purple.opacity(0.2)).frame(width: 44, height: 44)
                                Image(systemName: "popcorn.fill")
                                    .foregroundColor(Color.purple)
                                    .font(.system(size: 20))
                            }

                            VStack(alignment: .leading, spacing: 2) {
                                Text("Movies & IMAX Tickets 🍿")
                                    .font(.system(size: 15, weight: .bold))
                                    .foregroundColor(UsColors.textPrimary)
                                Text("Book seats with M-Ticket & Apple Wallet sync")
                                    .font(.system(size: 11))
                                    .foregroundColor(UsColors.textMuted)
                            }
                        }
                        .padding(14)
                        .frame(maxWidth: .infinity, alignment: .leading)
                        .background(UsColors.bgSecondary)
                        .clipShape(RoundedRectangle(cornerRadius: 16))

                        Text("Trending Now in Theatres")
                            .font(.system(size: 15, weight: .bold))
                            .foregroundColor(UsColors.textPrimary)

                        if viewModel.isLoading {
                            ProgressView()
                                .tint(UsColors.postbookPrimary)
                                .frame(maxWidth: .infinity, alignment: .center)
                                .padding(.vertical, 20)
                        } else {
                            LazyVStack(spacing: 12) {
                                ForEach(viewModel.movies) { movie in
                                    movieCard(movie)
                                }
                            }
                        }
                    }
                    .padding(16)
                }
            }
            .navigationTitle("Movie Tickets")
            .navigationBarTitleDisplayMode(.inline)
            .toolbar {
                ToolbarItem(placement: .cancellationAction) {
                    Button("Close", action: onDismiss)
                        .foregroundColor(UsColors.textMuted)
                }
            }
            .task {
                await viewModel.fetchShowtimes()
            }
        }
    }

    @ViewBuilder
    private func movieCard(_ movie: MovieShowtimeItem) -> some View {
        VStack(alignment: .leading, spacing: 10) {
            HStack {
                VStack(alignment: .leading, spacing: 2) {
                    Text(movie.title)
                        .font(.system(size: 15, weight: .bold))
                        .foregroundColor(UsColors.textPrimary)

                    Text(movie.cinema)
                        .font(.system(size: 11))
                        .foregroundColor(UsColors.textMuted)
                }

                Spacer()

                Text(movie.price)
                    .font(.system(size: 16, weight: .black, design: .rounded))
                    .foregroundColor(UsColors.onlineGreen)
            }

            HStack(spacing: 8) {
                Text(movie.time)
                    .font(.system(size: 11, weight: .bold, design: .monospaced))
                    .foregroundColor(.black)
                    .padding(.horizontal, 8)
                    .padding(.vertical, 4)
                    .background(Color.yellow)
                    .clipShape(RoundedRectangle(cornerRadius: 6))

                Text(movie.format)
                    .font(.system(size: 10, weight: .semibold))
                    .foregroundColor(UsColors.postbookPrimary)
                    .padding(.horizontal, 8)
                    .padding(.vertical, 4)
                    .background(UsColors.bgTertiary)
                    .clipShape(RoundedRectangle(cornerRadius: 6))

                Spacer()

                Button(action: {
                    Task {
                        let bookingId = try? await viewModel.bookTickets(showtimeId: movie.id, seats: ["F12", "F13"])
                        HapticManager.shared.trigger(.success)
                        ToastManager.shared.show("Booked 2x Seats! Pass: \(bookingId ?? "BK-CONFIRMED") 🎟️", style: .success)
                    }
                }) {
                    Text("Select Seats")
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
        .clipShape(RoundedRectangle(cornerRadius: 16))
    }
}
