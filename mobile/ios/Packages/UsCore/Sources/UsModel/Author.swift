import Foundation

public struct Author: Identifiable, Hashable, Codable, Sendable {
    public let id: String
    public let username: String?
    public let displayName: String?
    public let avatarUrl: String?

    public init(
        id: String,
        username: String? = nil,
        displayName: String? = nil,
        avatarUrl: String? = nil
    ) {
        self.id = id
        self.username = username
        self.displayName = displayName
        self.avatarUrl = avatarUrl
    }

    public var nameForDisplay: String {
        if let displayName = displayName, !displayName.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty {
            return displayName
        }
        if let username = username, !username.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty {
            return "@\(username)"
        }
        return "Someone"
    }

    enum CodingKeys: String, CodingKey {
        case id
        case username
        case displayName = "display_name"
        case avatarUrl = "avatar_url"
    }
}
