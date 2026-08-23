import Foundation

public struct PostCounts: Hashable, Codable, Sendable {
    public let likes: Int
    public let comments: Int
    public let reposts: Int

    public init(likes: Int = 0, comments: Int = 0, reposts: Int = 0) {
        self.likes = likes
        self.comments = comments
        self.reposts = reposts
    }

    enum CodingKeys: String, CodingKey {
        case likes
        case comments
        case reposts
    }

    public init(from decoder: Decoder) throws {
        let container = try decoder.container(keyedBy: CodingKeys.self)
        likes = try container.decodeIfPresent(Int.self, forKey: .likes) ?? 0
        comments = try container.decodeIfPresent(Int.self, forKey: .comments) ?? 0
        reposts = try container.decodeIfPresent(Int.self, forKey: .reposts) ?? 0
    }
}

public struct FeedViewerState: Hashable, Codable, Sendable {
    public let hasReacted: Bool
    public let isBookmarked: Bool
    public let hasReposted: Bool

    public init(hasReacted: Bool = false, isBookmarked: Bool = false, hasReposted: Bool = false) {
        self.hasReacted = hasReacted
        self.isBookmarked = isBookmarked
        self.hasReposted = hasReposted
    }
}

public struct FeedItem: Identifiable, Hashable, Codable, Sendable {
    public let id: String
    public let authorId: String
    public let author: Author
    public let text: String
    public let postType: String
    public let createdAt: String
    public let updatedAt: String
    public let isPinned: Bool
    public let counts: PostCounts
    public let viewCount: Int
    public let media: [FeedMedia]
    public let viewer: FeedViewerState
    public let isRepostable: Bool

    public init(
        id: String,
        authorId: String,
        author: Author,
        text: String,
        postType: String,
        createdAt: String,
        updatedAt: String,
        isPinned: Bool = false,
        counts: PostCounts = PostCounts(),
        viewCount: Int = 0,
        media: [FeedMedia] = [],
        viewer: FeedViewerState = FeedViewerState(),
        isRepostable: Bool = true
    ) {
        self.id = id
        self.authorId = authorId
        self.author = author
        self.text = text
        self.postType = postType
        self.createdAt = createdAt
        self.updatedAt = updatedAt
        self.isPinned = isPinned
        self.counts = counts
        self.viewCount = viewCount
        self.media = media
        self.viewer = viewer
        self.isRepostable = isRepostable
    }

    enum CodingKeys: String, CodingKey {
        case id
        case authorId = "author_id"
        case author
        case text
        case postType = "post_type"
        case createdAt = "created_at"
        case updatedAt = "updated_at"
        case isPinned = "is_pinned"
        case counts
        case viewCount = "view_count"
        case media
        case hasReacted = "has_reacted"
        case isBookmarked = "is_bookmarked"
        case repostCount = "repost_count"
        case hasReposted = "has_reposted"
        case isRepostable = "is_repostable"
    }

    public init(from decoder: Decoder) throws {
        let container = try decoder.container(keyedBy: CodingKeys.self)
        id = try container.decode(String.self, forKey: .id)
        authorId = try container.decode(String.self, forKey: .authorId)
        author = try container.decodeIfPresent(Author.self, forKey: .author) ?? Author(id: authorId)
        text = try container.decodeIfPresent(String.self, forKey: .text) ?? ""
        postType = try container.decodeIfPresent(String.self, forKey: .postType) ?? "text"
        createdAt = try container.decodeIfPresent(String.self, forKey: .createdAt) ?? ""
        updatedAt = try container.decodeIfPresent(String.self, forKey: .updatedAt) ?? ""
        isPinned = try container.decodeIfPresent(Bool.self, forKey: .isPinned) ?? false
        
        let rootCounts = try container.decodeIfPresent(PostCounts.self, forKey: .counts)
        let rootReposts = try container.decodeIfPresent(Int.self, forKey: .repostCount) ?? 0
        counts = PostCounts(
            likes: rootCounts?.likes ?? 0,
            comments: rootCounts?.comments ?? 0,
            reposts: rootCounts?.reposts ?? rootReposts
        )

        viewCount = try container.decodeIfPresent(Int.self, forKey: .viewCount) ?? 0
        media = try container.decodeIfPresent([FeedMedia].self, forKey: .media) ?? []

        let reacted = try container.decodeIfPresent(Bool.self, forKey: .hasReacted) ?? false
        let bookmarked = try container.decodeIfPresent(Bool.self, forKey: .isBookmarked) ?? false
        let reposted = try container.decodeIfPresent(Bool.self, forKey: .hasReposted) ?? false
        viewer = FeedViewerState(hasReacted: reacted, isBookmarked: bookmarked, hasReposted: reposted)

        isRepostable = try container.decodeIfPresent(Bool.self, forKey: .isRepostable) ?? true
    }

    public func encode(to encoder: Encoder) throws {
        var container = encoder.container(keyedBy: CodingKeys.self)
        try container.encode(id, forKey: .id)
        try container.encode(authorId, forKey: .authorId)
        try container.encode(author, forKey: .author)
        try container.encode(text, forKey: .text)
        try container.encode(postType, forKey: .postType)
        try container.encode(createdAt, forKey: .createdAt)
        try container.encode(updatedAt, forKey: .updatedAt)
        try container.encode(isPinned, forKey: .isPinned)
        try container.encode(counts, forKey: .counts)
        try container.encode(viewCount, forKey: .viewCount)
        try container.encode(media, forKey: .media)
        try container.encode(viewer.hasReacted, forKey: .hasReacted)
        try container.encode(viewer.isBookmarked, forKey: .isBookmarked)
        try container.encode(counts.reposts, forKey: .repostCount)
        try container.encode(viewer.hasReposted, forKey: .hasReposted)
        try container.encode(isRepostable, forKey: .isRepostable)
    }
}
