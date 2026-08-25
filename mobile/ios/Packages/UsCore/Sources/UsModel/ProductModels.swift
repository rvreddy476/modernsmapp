import Foundation

public struct Product: Identifiable, Hashable, Codable, Sendable {
    public let id: String
    public let title: String
    public let description: String
    public let pricePaise: Int64
    public let formattedPrice: String
    public let originalPricePaise: Int64?
    public let formattedOriginalPrice: String?
    public let imageUrls: [String]
    public let sellerName: String
    public let rating: Double
    public let reviewCount: Int
    public let category: String

    public init(
        id: String,
        title: String,
        description: String,
        pricePaise: Int64,
        formattedPrice: String,
        originalPricePaise: Int64? = nil,
        formattedOriginalPrice: String? = nil,
        imageUrls: [String] = [],
        sellerName: String = "Official Store",
        rating: Double = 4.8,
        reviewCount: Int = 340,
        category: String = "Electronics"
    ) {
        self.id = id
        self.title = title
        self.description = description
        self.pricePaise = pricePaise
        self.formattedPrice = formattedPrice
        self.originalPricePaise = originalPricePaise
        self.formattedOriginalPrice = formattedOriginalPrice
        self.imageUrls = imageUrls
        self.sellerName = sellerName
        self.rating = rating
        self.reviewCount = reviewCount
        self.category = category
    }
}

public struct CartItem: Identifiable, Hashable, Codable, Sendable {
    public let product: Product
    public var quantity: Int

    public var id: String { product.id }

    public init(product: Product, quantity: Int = 1) {
        self.product = product
        self.quantity = quantity
    }
}
