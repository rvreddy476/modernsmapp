import SwiftUI
import UsModel
import UsDesignSystem
import UsNetwork

public struct LiveShoppingOverlayView: View {
    public let product: LiveProductPin
    public let onBuyNow: (LiveProductPin) -> Void

    public init(
        product: LiveProductPin = LiveProductPin(
            id: "lp1",
            title: "Limited Edition Oversized Acid Wash Jacket",
            pricePaise: 199900,
            formattedPrice: "₹1,999",
            originalPrice: "₹3,999",
            imageUrl: "https://images.unsplash.com/photo-1551028719-00167b16eac5?w=600",
            discountTag: "FLASH SALE 50% OFF",
            stockRemaining: 4
        ),
        onBuyNow: @escaping (LiveProductPin) -> Void = { _ in }
    ) {
        self.product = product
        self.onBuyNow = onBuyNow
    }

    public var body: some View {
        HStack(spacing: 12) {
            // Product Thumbnail
            if let url = URL(string: product.imageUrl) {
                AsyncImage(url: url) { phase in
                    switch phase {
                    case .success(let img):
                        img.resizable().scaledToFill()
                    default:
                        Rectangle().fill(UsColors.bgTertiary)
                    }
                }
                .frame(width: 60, height: 60)
                .clipShape(RoundedRectangle(cornerRadius: 10))
            }

            // Info
            VStack(alignment: .leading, spacing: 3) {
                Text(product.discountTag)
                    .font(.system(size: 9, weight: .black))
                    .foregroundColor(.white)
                    .padding(.horizontal, 6)
                    .padding(.vertical, 2)
                    .background(UsColors.liveRed)
                    .clipShape(Capsule())

                Text(product.title)
                    .font(.system(size: 13, weight: .semibold))
                    .foregroundColor(.white)
                    .lineLimit(1)

                HStack(spacing: 6) {
                    Text(product.formattedPrice)
                        .font(.system(size: 14, weight: .bold, design: .rounded))
                        .foregroundColor(.white)

                    if let orig = product.originalPrice {
                        Text(orig)
                            .font(.system(size: 11))
                            .strikethrough()
                            .foregroundColor(.white.opacity(0.6))
                    }

                    Text("• Only \(product.stockRemaining) left!")
                        .font(.system(size: 11, weight: .medium))
                        .foregroundColor(Color.orange)
                }
            }

            Spacer()

            // Buy Now button
            Button(action: {
                HapticManager.shared.trigger(.success)
                onBuyNow(product)
                ToastManager.shared.show("Item Reserved! Checkout via UPI", style: .success)
            }) {
                Text("Buy")
                    .font(.system(size: 13, weight: .bold))
                    .foregroundColor(.black)
                    .padding(.horizontal, 16)
                    .padding(.vertical, 10)
                    .background(Color.white)
                    .clipShape(Capsule())
            }
        }
        .padding(12)
        .background(Color.black.opacity(0.75))
        .clipShape(RoundedRectangle(cornerRadius: 16))
        .overlay(RoundedRectangle(cornerRadius: 16).stroke(Color.white.opacity(0.2), lineWidth: 1))
        .padding(.horizontal, 16)
    }
}
