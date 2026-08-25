import SwiftUI
import UsModel
import UsDesignSystem
import UsNetwork

public struct BookListingItem: Identifiable {
    public let id: String
    public let title: String
    public let author: String
    public let ownerName: String
    public let genre: String
    public let distanceKm: Double

    public init(id: String, title: String, author: String, ownerName: String, genre: String, distanceKm: Double) {
        self.id = id
        self.title = title
        self.author = author
        self.ownerName = ownerName
        self.genre = genre
        self.distanceKm = distanceKm
    }
}

public struct BookExchangeView: View {
    public let onDismiss: () -> Void

    @State private var books: [BookListingItem] = [
        BookListingItem(id: "bk-1", title: "Atomic Habits", author: "James Clear", ownerName: "Kavya Patel", genre: "Self Help", distanceKm: 0.8),
        BookListingItem(id: "bk-2", title: "The Psychology of Money", author: "Morgan Housel", ownerName: "Alex Rivera", genre: "Finance", distanceKm: 1.4),
        BookListingItem(id: "bk-3", title: "Project Hail Mary", author: "Andy Weir", ownerName: "Rohan Nair", genre: "Sci-Fi", distanceKm: 2.1)
    ]

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
                        // Banner
                        HStack(spacing: 12) {
                            ZStack {
                                Circle().fill(UsColors.postbookPrimary.opacity(0.2)).frame(width: 44, height: 44)
                                Image(systemName: "books.vertical.fill")
                                    .foregroundColor(UsColors.postbookPrimary)
                                    .font(.system(size: 20))
                            }

                            VStack(alignment: .leading, spacing: 2) {
                                Text("Hyperlocal Book Swap & Exchange 📚")
                                    .font(.system(size: 15, weight: .bold))
                                    .foregroundColor(UsColors.textPrimary)
                                Text("Borrow & lend physical books with nearby readers")
                                    .font(.system(size: 11))
                                    .foregroundColor(UsColors.textMuted)
                            }
                        }
                        .padding(14)
                        .frame(maxWidth: .infinity, alignment: .leading)
                        .background(UsColors.bgSecondary)
                        .clipShape(RoundedRectangle(cornerRadius: 16))

                        Text("Available for Swap Nearby")
                            .font(.system(size: 15, weight: .bold))
                            .foregroundColor(UsColors.textPrimary)

                        LazyVStack(spacing: 12) {
                            ForEach(books) { book in
                                bookRow(book)
                            }
                        }
                    }
                    .padding(16)
                }
            }
            .navigationTitle("Book Exchange")
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
    private func bookRow(_ book: BookListingItem) -> some View {
        VStack(alignment: .leading, spacing: 10) {
            HStack {
                VStack(alignment: .leading, spacing: 2) {
                    Text(book.title)
                        .font(.system(size: 15, weight: .bold))
                        .foregroundColor(UsColors.textPrimary)

                    Text("by \(book.author) • \(book.genre)")
                        .font(.system(size: 12))
                        .foregroundColor(UsColors.textMuted)
                }

                Spacer()

                Text(String(format: "%.1f km", book.distanceKm))
                    .font(.system(size: 11))
                    .foregroundColor(UsColors.postbookPrimary)
            }

            Divider().background(UsColors.borderSubtle)

            HStack {
                Text("Offered by \(book.ownerName)")
                    .font(.system(size: 11))
                    .foregroundColor(UsColors.textMuted)

                Spacer()

                Button(action: {
                    HapticManager.shared.trigger(.success)
                    ToastManager.shared.show("Swap request sent to \(book.ownerName)!", style: .success)
                }) {
                    Text("Request Swap")
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
