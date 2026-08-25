import Foundation

public struct StoryItem: Identifiable, Hashable, Codable, Sendable {
    public let id: String
    public let authorId: String
    public let mediaUrl: String
    public let mediaType: String // "image" or "video"
    public let duration: Double // in seconds, default 5.0
    public let createdAt: String
    public let expiresAt: String
    public var isViewed: Bool

    public init(
        id: String,
        authorId: String,
        mediaUrl: String,
        mediaType: String = "image",
        duration: Double = 5.0,
        createdAt: String,
        expiresAt: String,
        isViewed: Bool = false
    ) {
        self.id = id
        self.authorId = authorId
        self.mediaUrl = mediaUrl
        self.mediaType = mediaType
        self.duration = duration
        self.createdAt = createdAt
        self.expiresAt = expiresAt
        self.isViewed = isViewed
    }

    enum CodingKeys: String, CodingKey {
        case id
        case authorId = "author_id"
        case mediaUrl = "media_url"
        case mediaType = "media_type"
        case duration
        case createdAt = "created_at"
        case expiresAt = "expires_at"
        case isViewed = "is_viewed"
    }
}

public struct UserStories: Identifiable, Hashable, Codable, Sendable {
    public let id: String // authorId
    public let author: Author
    public var stories: [StoryItem]

    public init(id: String, author: Author, stories: [StoryItem]) {
        self.id = id
        self.author = author
        self.stories = stories
    }

    public var hasUnviewed: Bool {
        stories.contains { !$0.isViewed }
    }
}
