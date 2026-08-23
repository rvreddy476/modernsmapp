import Foundation

public struct LiveProductPin: Identifiable, Hashable, Codable, Sendable {
    public let id: String
    public let title: String
    public let pricePaise: Int64
    public let formattedPrice: String
    public let originalPrice: String?
    public let imageUrl: String
    public let discountTag: String
    public let stockRemaining: Int

    public init(
        id: String,
        title: String,
        pricePaise: Int64,
        formattedPrice: String,
        originalPrice: String? = nil,
        imageUrl: String = "",
        discountTag: String = "50% OFF",
        stockRemaining: Int = 8
    ) {
        self.id = id
        self.title = title
        self.pricePaise = pricePaise
        self.formattedPrice = formattedPrice
        self.originalPrice = originalPrice
        self.imageUrl = imageUrl
        self.discountTag = discountTag
        self.stockRemaining = stockRemaining
    }
}
