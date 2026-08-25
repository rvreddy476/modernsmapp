import Foundation

public struct Restaurant: Identifiable, Hashable, Codable, Sendable {
    public let id: String
    public let name: String
    public let cuisine: String
    public let rating: Double
    public let deliveryTimeMins: Int
    public let priceForTwo: String
    public let imageUrl: String
    public let isPureVeg: Bool

    public init(
        id: String,
        name: String,
        cuisine: String,
        rating: Double = 4.6,
        deliveryTimeMins: Int = 28,
        priceForTwo: String = "₹400 for two",
        imageUrl: String = "",
        isPureVeg: Bool = false
    ) {
        self.id = id
        self.name = name
        self.cuisine = cuisine
        self.rating = rating
        self.deliveryTimeMins = deliveryTimeMins
        self.priceForTwo = priceForTwo
        self.imageUrl = imageUrl
        self.isPureVeg = isPureVeg
    }
}

public struct FoodMenuItem: Identifiable, Hashable, Codable, Sendable {
    public let id: String
    public let name: String
    public let description: String
    public let pricePaise: Int64
    public let formattedPrice: String
    public let isVeg: Bool
    public let isBestseller: Bool
    public let imageUrl: String?

    public init(
        id: String,
        name: String,
        description: String,
        pricePaise: Int64,
        formattedPrice: String,
        isVeg: Bool = true,
        isBestseller: Bool = false,
        imageUrl: String? = nil
    ) {
        self.id = id
        self.name = name
        self.description = description
        self.pricePaise = pricePaise
        self.formattedPrice = formattedPrice
        self.isVeg = isVeg
        self.isBestseller = isBestseller
        self.imageUrl = imageUrl
    }
}
