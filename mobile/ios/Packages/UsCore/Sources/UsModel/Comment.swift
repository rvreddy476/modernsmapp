import Foundation

public struct Comment: Identifiable, Hashable, Codable, Sendable {
    public let id: String
    public let postId: String
    public let userId: String
    public let author: Author
    public let text: String
    public let createdAt: String
    public let likeCount: Int
    public let hasLiked: Bool

    public init(
        id: String,
        postId: String,
        userId: String,
        author: Author,
        text: String,
        createdAt: String,
        likeCount: Int = 0,
        hasLiked: Bool = false
    ) {
        self.id = id
        self.postId = postId
        self.userId = userId
        self.author = author
        self.text = text
        self.createdAt = createdAt
        self.likeCount = likeCount
        self.hasLiked = hasLiked
    }

    enum CodingKeys: String, CodingKey {
        case id
        case postId = "post_id"
        case userId = "user_id"
        case author
        case text
        case createdAt = "created_at"
        case likeCount = "like_count"
        case hasLiked = "has_liked"
    }

    public init(from decoder: Decoder) throws {
        let container = try decoder.container(keyedBy: CodingKeys.self)
        id = try container.decode(String.self, forKey: .id)
        postId = try container.decode(String.self, forKey: .postId)
        userId = try container.decode(String.self, forKey: .userId)
        author = try container.decodeIfPresent(Author.self, forKey: .author) ?? Author(id: userId)
        text = try container.decode(String.self, forKey: .text)
        createdAt = try container.decodeIfPresent(String.self, forKey: .createdAt) ?? ""
        likeCount = try container.decodeIfPresent(Int.self, forKey: .likeCount) ?? 0
        hasLiked = try container.decodeIfPresent(Bool.self, forKey: .hasLiked) ?? false
    }
}
