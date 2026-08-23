import SwiftUI
import UsModel
import UsDesignSystem
import UsNetwork

@Observable
public final class EventsViewModel: @unchecked Sendable {
    public var events: [EventItem] = []
    public var selectedCategory: String = "All"
    public var isLoading: Bool = false

    private let client: APIClientProtocol
    public let categories = ["All", "Tech", "Standup Comedy", "Music", "Meetups", "Art"]

    public init(client: APIClientProtocol = APIClient()) {
        self.client = client
        populateMockEvents()
    }

    public var filteredEvents: [EventItem] {
        if selectedCategory == "All" { return events }
        return events.filter { $0.category == selectedCategory }
    }

    private func populateMockEvents() {
        events = [
            EventItem(
                id: "ev1",
                title: "India AI & Super-App Builders Summit 2026",
                organizer: "US Creator Foundation",
                venue: "Bangalore International Centre, Domlur",
                dateString: "Sat, Sep 12 • 10:00 AM",
                pricePaise: 49900,
                formattedPrice: "₹499",
                category: "Tech",
                imageUrl: "https://images.unsplash.com/photo-1540575467063-178a50c2df87?w=800",
                attendeeCount: 450
            ),
            EventItem(
                id: "ev2",
                title: "Anubhav Singh Bassi - Live Standup Special",
                organizer: "Punchliners India",
                venue: "Chowdiah Memorial Hall, Malleshwaram",
                dateString: "Sun, Sep 20 • 7:30 PM",
                pricePaise: 99900,
                formattedPrice: "₹999",
                category: "Standup Comedy",
                imageUrl: "https://images.unsplash.com/photo-1514525253161-7a46d19cd819?w=800",
                attendeeCount: 1200
            ),
            EventItem(
                id: "ev3",
                title: "Prateek Kuhad - Acoustic Sunset Session",
                organizer: "Indie Wave Music",
                venue: "Jayant Memorial Open Amphitheatre",
                dateString: "Fri, Oct 2 • 6:00 PM",
                pricePaise: 149900,
                formattedPrice: "₹1,499",
                category: "Music",
                imageUrl: "https://images.unsplash.com/photo-1470225620780-dba8ba36b745?w=800",
                attendeeCount: 850
            )
        ]
    }
}

public struct EventsDiscoveryView: View {
    @State private var viewModel: EventsViewModel
    @State private var selectedEvent: EventItem? = nil
    @State private var bookedTicketEvent: EventItem? = nil
    public let onDismiss: () -> Void

    public init(client: APIClientProtocol = APIClient(), onDismiss: @escaping () -> Void = {}) {
        _viewModel = State(initialValue: EventsViewModel(client: client))
        self.onDismiss = onDismiss
    }

    public var body: some View {
        NavigationStack {
            ZStack {
                UsColors.bgPrimary
                    .ignoresSafeArea()

                ScrollView {
                    VStack(alignment: .leading, spacing: 18) {
                        // Category pills
                        categoryFilterRow

                        // Events feed
                        Text("Trending Events in Bangalore")
                            .font(.system(size: 17, weight: .bold))
                            .foregroundColor(UsColors.textPrimary)

                        LazyVStack(spacing: 16) {
                            ForEach(viewModel.filteredEvents) { event in
                                eventCard(event)
                            }
                        }
                    }
                    .padding(16)
                }
            }
            .navigationTitle("Events & Tickets")
            .navigationBarTitleDisplayMode(.inline)
            .toolbar {
                ToolbarItem(placement: .cancellationAction) {
                    Button("Close", action: onDismiss)
                        .foregroundColor(UsColors.textMuted)
                }
            }
            .sheet(item: $selectedEvent) { ev in
                eventDetailSheet(ev)
            }
            .sheet(item: $bookedTicketEvent) { ev in
                ticketPassSheet(ev)
            }
        }
    }

    private var categoryFilterRow: some View {
        ScrollView(.horizontal, showsIndicators: false) {
            HStack(spacing: 8) {
                ForEach(viewModel.categories, id: \.self) { cat in
                    let isSelected = viewModel.selectedCategory == cat
                    Button(action: { viewModel.selectedCategory = cat }) {
                        Text(cat)
                            .font(.system(size: 13, weight: .semibold))
                            .foregroundColor(isSelected ? .black : UsColors.textPrimary)
                            .padding(.horizontal, 16)
                            .padding(.vertical, 8)
                            .background(isSelected ? Color.white : UsColors.bgSecondary)
                            .clipShape(Capsule())
                    }
                    .buttonStyle(.plain)
                }
            }
        }
    }

    @ViewBuilder
    private func eventCard(_ event: EventItem) -> some View {
        Button(action: { selectedEvent = event }) {
            VStack(alignment: .leading, spacing: 12) {
                ZStack(alignment: .bottomLeading) {
                    if let url = URL(string: event.imageUrl) {
                        AsyncImage(url: url) { phase in
                            switch phase {
                            case .success(let img):
                                img.resizable().scaledToFill()
                            default:
                                Rectangle().fill(UsColors.bgTertiary)
                            }
                        }
                    } else {
                        Rectangle().fill(UsColors.bgTertiary)
                    }

                    // Price tag badge
                    Text(event.formattedPrice)
                        .font(.system(size: 13, weight: .black, design: .rounded))
                        .foregroundColor(.black)
                        .padding(.horizontal, 10)
                        .padding(.vertical, 6)
                        .background(Color.white)
                        .clipShape(Capsule())
                        .padding(12)
                }
                .frame(height: 170)
                .clipShape(RoundedRectangle(cornerRadius: 14))

                VStack(alignment: .leading, spacing: 4) {
                    Text(event.dateString)
                        .font(.system(size: 12, weight: .bold))
                        .foregroundColor(UsColors.postbookPrimary)

                    Text(event.title)
                        .font(.system(size: 16, weight: .bold))
                        .foregroundColor(UsColors.textPrimary)
                        .lineLimit(2)

                    Text(event.venue)
                        .font(.system(size: 12))
                        .foregroundColor(UsColors.textMuted)
                        .lineLimit(1)
                }
            }
            .padding(12)
            .background(UsColors.bgSecondary)
            .clipShape(RoundedRectangle(cornerRadius: 18))
        }
        .buttonStyle(.plain)
    }

    @ViewBuilder
    private func eventDetailSheet(_ event: EventItem) -> some View {
        NavigationStack {
            ZStack {
                UsColors.bgPrimary.ignoresSafeArea()

                VStack(spacing: 20) {
                    ScrollView {
                        VStack(alignment: .leading, spacing: 14) {
                            if let url = URL(string: event.imageUrl) {
                                AsyncImage(url: url) { phase in
                                    if let img = phase.image {
                                        img.resizable().scaledToFill()
                                    }
                                }
                                .frame(height: 200)
                                .clipShape(RoundedRectangle(cornerRadius: 14))
                            }

                            Text(event.title)
                                .font(.system(size: 20, weight: .bold))
                                .foregroundColor(UsColors.textPrimary)

                            HStack(spacing: 8) {
                                Image(systemName: "calendar")
                                    .foregroundColor(UsColors.postbookPrimary)
                                Text(event.dateString)
                                    .font(.system(size: 13, weight: .medium))
                                    .foregroundColor(UsColors.textPrimary)
                            }

                            HStack(spacing: 8) {
                                Image(systemName: "mappin.and.ellipse")
                                    .foregroundColor(UsColors.postgramPrimary)
                                Text(event.venue)
                                    .font(.system(size: 13))
                                    .foregroundColor(UsColors.textMuted)
                            }

                            Divider().background(UsColors.borderSubtle)

                            Text("About Event")
                                .font(.system(size: 16, weight: .bold))
                                .foregroundColor(UsColors.textPrimary)

                            Text("Join \(event.attendeeCount)+ creators, engineers, and enthusiasts for an unforgettable evening. Instant digital QR entry with your US App ticket.")
                                .font(.system(size: 14))
                                .foregroundColor(UsColors.textSecondary)
                                .lineSpacing(3)
                        }
                        .padding(16)
                    }

                    // Book Ticket Button
                    Button(action: {
                        selectedEvent = nil
                        DispatchQueue.main.asyncAfter(deadline: .now() + 0.3) {
                            bookedTicketEvent = event
                        }
                    }) {
                        HStack {
                            Spacer()
                            Text("Book 1x Pass for \(event.formattedPrice) (UPI)")
                                .font(.system(size: 15, weight: .bold))
                                .foregroundColor(.black)
                            Spacer()
                        }
                        .padding(.vertical, 16)
                        .background(Color.white)
                        .clipShape(RoundedRectangle(cornerRadius: 14))
                    }
                    .padding(16)
                }
            }
            .navigationBarTitleDisplayMode(.inline)
            .toolbar {
                ToolbarItem(placement: .cancellationAction) {
                    Button("Close") { selectedEvent = nil }
                        .foregroundColor(UsColors.textMuted)
                }
            }
        }
    }

    @ViewBuilder
    private func ticketPassSheet(_ event: EventItem) -> some View {
        NavigationStack {
            ZStack {
                UsColors.bgPrimary.ignoresSafeArea()

                VStack(spacing: 24) {
                    VStack(spacing: 8) {
                        Image(systemName: "checkmark.circle.fill")
                            .font(.system(size: 48))
                            .foregroundColor(UsColors.onlineGreen)

                        Text("Booking Confirmed!")
                            .font(.system(size: 22, weight: .bold))
                            .foregroundColor(UsColors.textPrimary)
                    }
                    .padding(.top, 12)

                    // Ticket Card
                    VStack(spacing: 16) {
                        VStack(spacing: 4) {
                            Text(event.title)
                                .font(.system(size: 16, weight: .bold))
                                .foregroundColor(UsColors.textPrimary)
                                .multilineTextAlignment(.center)
                            Text(event.dateString)
                                .font(.system(size: 13))
                                .foregroundColor(UsColors.postbookPrimary)
                        }

                        Divider().background(UsColors.borderSubtle)

                        // Ticket QR Code
                        ZStack {
                            Rectangle().fill(Color.white).frame(width: 160, height: 160)
                            Image(systemName: "qrcode")
                                .font(.system(size: 140))
                                .foregroundColor(.black)
                        }
                        .clipShape(RoundedRectangle(cornerRadius: 12))

                        Text("Pass ID: US-TKT-9482-BGL")
                            .font(.system(size: 12, weight: .monospaced))
                            .foregroundColor(UsColors.textMuted)
                    }
                    .padding(20)
                    .background(UsColors.bgSecondary)
                    .clipShape(RoundedRectangle(cornerRadius: 20))
                    .overlay(RoundedRectangle(cornerRadius: 20).stroke(UsColors.borderMedium, lineWidth: 1))
                    .padding(.horizontal, 24)

                    Spacer()
                }
                .padding(16)
            }
            .navigationTitle("Digital Ticket")
            .navigationBarTitleDisplayMode(.inline)
            .toolbar {
                ToolbarItem(placement: .cancellationAction) {
                    Button("Done") { bookedTicketEvent = nil }
                        .foregroundColor(UsColors.textMuted)
                }
            }
        }
    }
}
