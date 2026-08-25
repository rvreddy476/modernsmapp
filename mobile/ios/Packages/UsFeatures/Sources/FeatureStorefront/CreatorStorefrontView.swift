import SwiftUI
import UsModel
import UsDesignSystem
import UsNetwork

public struct StoreProductItem: Identifiable, Hashable {
    public let id: String
    public let title: String
    public let price: String
    public let category: String
    public let imageUrl: String
    public let isDigital: Bool

    public init(
        id: String,
        title: String,
        price: String,
        category: String = "Merchandise",
        imageUrl: String = "https://images.unsplash.com/photo-1556905055-8f358a7a47b2?w=800",
        isDigital: Bool = false
    ) {
        self.id = id
        self.title = title
        self.price = price
        self.category = category
        self.imageUrl = imageUrl
        self.isDigital = isDigital
    }
}

public struct CreatorStorefrontView: View {
    public let creator: Author
    public let onDismiss: () -> Void

    @State private var products: [StoreProductItem] = [
        StoreProductItem(id: "sp-1", title: "Oversized Cyber-Hoodie (Black)", price: "₹2,199", category: "Merch", imageUrl: "https://images.unsplash.com/photo-1556905055-8f358a7a47b2?w=800"),
        StoreProductItem(id: "sp-2", title: "Cinematic Film LUTs & Lightroom Presets Pack", price: "₹799", category: "Digital", imageUrl: "https://images.unsplash.com/photo-1516035069371-29a1b244cc32?w=800", isDigital: true),
        StoreProductItem(id: "sp-3", title: "1-on-1 Creator Strategy Call (45 Mins)", price: "₹3,499", category: "Services", imageUrl: "https://images.unsplash.com/photo-1517841905240-472988babdf9?w=800", isDigital: true)
    ]

    public init(
        creator: Author = Author(id: "c1", username: "sarah_c", displayName: "Sarah Chen"),
        onDismiss: @escaping () -> Void = {}
    ) {
        self.creator = creator
        self.onDismiss = onDismiss
    }

    public var body: some View {
        NavigationStack {
            ZStack {
                UsColors.bgPrimary
                    .ignoresSafeArea()

                ScrollView {
                    VStack(alignment: .leading, spacing: 18) {
                        // Storefront Banner
                        HStack(spacing: 14) {
                            UsAvatar(name: creator.nameForDisplay, url: creator.avatarUrl, size: .large)

                            VStack(alignment: .leading, spacing: 2) {
                                Text("\(creator.nameForDisplay)'s Store")
                                    .font(.system(size: 18, weight: .bold))
                                    .foregroundColor(UsColors.textPrimary)
                                Text("Official Merchandise & Digital Drops")
                                    .font(.system(size: 12))
                                    .foregroundColor(UsColors.textMuted)
                            }
                        }
                        .padding(16)
                        .frame(maxWidth: .infinity, alignment: .leading)
                        .background(UsColors.bgSecondary)
                        .clipShape(RoundedRectangle(cornerRadius: 16))

                        Text("Products & Digital Drops")
                            .font(.system(size: 16, weight: .bold))
                            .foregroundColor(UsColors.textPrimary)

                        LazyVStack(spacing: 12) {
                            ForEach(products) { product in
                                productRow(product)
                            }
                        }
                    }
                    .padding(16)
                }
            }
            .navigationTitle("Creator Store")
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
    private func productRow(_ product: StoreProductItem) -> some View {
        HStack(spacing: 12) {
            if let url = URL(string: product.imageUrl) {
                AsyncImage(url: url) { phase in
                    if let img = phase.image {
                        img.resizable().scaledToFill()
                    } else {
                        Rectangle().fill(UsColors.bgTertiary)
                    }
                }
                .frame(width: 74, height: 74)
                .clipShape(RoundedRectangle(cornerRadius: 12))
            }

            VStack(alignment: .leading, spacing: 4) {
                Text(product.title)
                    .font(.system(size: 14, weight: .semibold))
                    .foregroundColor(UsColors.textPrimary)
                    .lineLimit(2)

                Text(product.price)
                    .font(.system(size: 16, weight: .black, design: .rounded))
                    .foregroundColor(UsColors.onlineGreen)
            }

            Spacer()

            Button(action: {
                HapticManager.shared.trigger(.success)
                ToastManager.shared.show("Ordered \(product.title) via US UPI!", style: .success)
            }) {
                Text("Buy Now")
                    .font(.system(size: 12, weight: .bold))
                    .foregroundColor(.black)
                    .padding(.horizontal, 14)
                    .padding(.vertical, 8)
                    .background(Color.white)
                    .clipShape(Capsule())
            }
        }
        .padding(12)
        .background(UsColors.bgSecondary)
        .clipShape(RoundedRectangle(cornerRadius: 16))
    }
}
