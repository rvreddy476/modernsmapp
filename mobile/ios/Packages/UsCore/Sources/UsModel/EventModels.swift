import Foundation

public struct EventItem: Identifiable, Hashable, Codable, Sendable {
    public let id: String
    public let title: String
    public let organizer: String
    public let venue: String
    public let dateString: String
    public let pricePaise: Int64
    public let formattedPrice: String
    public let category: String
    public let imageUrl: String
    public let attendeeCount: Int

    public init(
        id: String,
        title: String,
        organizer: String,
        venue: String,
        dateString: String,
        pricePaise: Int64,
        formattedPrice: String,
        category: String = "Tech",
        imageUrl: String = "",
        attendeeCount: Int = 180
    ) {
        self.id = id
        self.title = title
        self.organizer = organizer
        self.venue = venue
        self.dateString = dateString
        self.pricePaise = pricePaise
        self.formattedPrice = formattedPrice
        self.category = category
        self.imageUrl = imageUrl
        self.attendeeCount = attendeeCount
    }
}
