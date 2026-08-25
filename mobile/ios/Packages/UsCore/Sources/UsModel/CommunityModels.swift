import Foundation

public struct Community: Identifiable, Hashable, Codable, Sendable {
    public let id: String
    public let name: String
    public let description: String
    public let bannerUrl: String
    public let iconUrl: String
    public let membersCount: Int
    public let isJoined: Bool
    public let category: String

    public init(
        id: String,
        name: String,
        description: String,
        bannerUrl: String = "",
        iconUrl: String = "",
        membersCount: Int = 1200,
        isJoined: Bool = false,
        category: String = "Tech"
    ) {
        self.id = id
        self.name = name
        self.description = description
        self.bannerUrl = bannerUrl
        self.iconUrl = iconUrl
        self.membersCount = membersCount
        self.isJoined = isJoined
        self.category = category
    }
}
