import SwiftUI
import UsModel
import UsDesignSystem
import UsNetwork

public struct ClassifiedItem: Identifiable, Hashable {
    public let id: String
    public let title: String
    public let price: String
    public let condition: String
    public let location: String
    public let imageUrl: String
    public let sellerName: String

    public init(
        id: String,
        title: String,
        price: String,
        condition: String = "Like New",
        location: String = "Indiranagar, Bangalore (1.2 km)",
        imageUrl: String = "https://images.unsplash.com/photo-1505740420928-5e560c06d30e?w=800",
        sellerName: String = "Rahul"
    ) {
        self.id = id
        self.title = title
        self.price = price
        self.condition = condition
        self.location = location
        self.imageUrl = imageUrl
        self.sellerName = sellerName
    }
}

public struct ClassifiedsListView: View {
    public let onDismiss: () -> Void

    @State private var items: [ClassifiedItem] = [
        ClassifiedItem(id: "cl-1", title: "Sony WH-1000XM4 Noise Canceling Headphones", price: "₹14,500", condition: "Like New", location: "Koramangala 5th Block (800m)", imageUrl: "https://images.unsplash.com/photo-1505740420928-5e560c06d30e?w=800", sellerName: "Aanya"),
        ClassifiedItem(id: "cl-2", title: "Fujifilm X-T30 Mirrorless Camera + 18-55mm Kit", price: "₹48,000", condition: "Gently Used", location: "Indiranagar 100ft Rd (1.8 km)", imageUrl: "https://images.unsplash.com/photo-1516035069371-29a1b244cc32?w=800", sellerName: "Marcus"),
        ClassifiedItem(id: "cl-3", title: "Apple iPad Mini 6th Gen 64GB Space Gray", price: "₹31,000", condition: "Mint Box Pack", location: "HSR Layout Sector 2 (3.1 km)", imageUrl: "https://images.unsplash.com/photo-1544244015-0df4b3ffc6b0?w=800", sellerName: "Sarah")
    ]

    private let columns = [
        GridItem(.flexible(), spacing: 14),
        GridItem(.flexible(), spacing: 14)
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
                    VStack(alignment: .leading, spacing: 16) {
                        Text("Buy & Sell Pre-Owned Items Nearby")
                            .font(.system(size: 13))
                            .foregroundColor(UsColors.textMuted)

                        LazyVGrid(columns: columns, spacing: 14) {
                            ForEach(items) { item in
                                classifiedCard(item)
                            }
                        }
                    }
                    .padding(16)
                }
            }
            .navigationTitle("Flea Market & Deals")
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
    private func classifiedCard(_ item: ClassifiedItem) -> some View {
        Button(action: {
            HapticManager.shared.trigger(.selection)
            ToastManager.shared.show("Opening chat with \(item.sellerName)...", style: .info)
        }) {
            VStack(alignment: .leading, spacing: 8) {
                ZStack(alignment: .topTrailing) {
                    if let url = URL(string: item.imageUrl) {
                        AsyncImage(url: url) { phase in
                            if let img = phase.image {
                                img.resizable().scaledToFill()
                            } else {
                                Rectangle().fill(UsColors.bgTertiary)
                            }
                        }
                        .frame(height: 120)
                        .clipShape(RoundedRectangle(cornerRadius: 12))
                    }

                    Text(item.condition)
                        .font(.system(size: 9, weight: .bold))
                        .foregroundColor(.black)
                        .padding(.horizontal, 6)
                        .padding(.vertical, 3)
                        .background(Color.yellow)
                        .clipShape(Capsule())
                        .padding(6)
                }

                VStack(alignment: .leading, spacing: 2) {
                    Text(item.price)
                        .font(.system(size: 16, weight: .black, design: .rounded))
                        .foregroundColor(UsColors.onlineGreen)

                    Text(item.title)
                        .font(.system(size: 12, weight: .semibold))
                        .foregroundColor(UsColors.textPrimary)
                        .lineLimit(2)

                    Text(item.location)
                        .font(.system(size: 10))
                        .foregroundColor(UsColors.textMuted)
                        .lineLimit(1)
                }
            }
            .padding(10)
            .background(UsColors.bgSecondary)
            .clipShape(RoundedRectangle(cornerRadius: 16))
        }
        .buttonStyle(.plain)
    }
}
