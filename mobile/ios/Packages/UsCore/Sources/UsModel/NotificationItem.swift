import Foundation

public enum NotificationType: String, Codable, Sendable {
    case like
    case reaction
    case comment
    case follow
    case mention
    case repost
    case unknown
}

public struct NotificationAddress: Hashable, Codable, Sendable {
    public let bucket: Int
    public let ts: String

    public init(bucket: Int, ts: String) {
        self.bucket = bucket
        self.ts = ts
    }
}

public struct NotificationItem: Identifiable, Hashable, Codable, Sendable {
    public var id: String { notificationId ?? "\(bucket)_\(ts)" }
    public let notificationId: String?
    public let userId: String?
    public let bucket: Int
    public let ts: String
    public let type: String
    public let actorUserId: String?
    public let actor: Author?
    public let entityType: String?
    public let entityId: String?
    public let deepLink: String?
    public let commentText: String?
    public let createdAt: String?
    public var isRead: Bool

    public init(
        notificationId: String? = nil,
        userId: String? = nil,
        bucket: Int = 0,
        ts: String = "",
        type: String = "general",
        actorUserId: String? = nil,
        actor: Author? = nil,
        entityType: String? = nil,
        entityId: String? = nil,
        deepLink: String? = nil,
        commentText: String? = nil,
        createdAt: String? = nil,
        isRead: Bool = false
    ) {
        self.notificationId = notificationId
        self.userId = userId
        self.bucket = bucket
        self.ts = ts
        self.type = type
        self.actorUserId = actorUserId
        self.actor = actor
        self.entityType = entityType
        self.entityId = entityId
        self.deepLink = deepLink
        self.commentText = commentText
        self.createdAt = createdAt
        self.isRead = isRead
    }

    enum CodingKeys: String, CodingKey {
        case notificationId = "notification_id"
        case userId = "user_id"
        case bucket
        case ts
        case type
        case actorUserId = "actor_user_id"
        case actor
        case entityType = "entity_type"
        case entityId = "entity_id"
        case deepLink = "deep_link"
        case commentText = "comment_text"
        case createdAt = "created_at"
        case isRead = "is_read"
    }

    public var displayTitle: String {
        switch type {
        case "reaction", "like":
            return "Someone reacted to your post."
        case "comment":
            return "Someone commented on your post."
        case "follow", "user_followed":
            return "Someone started following you."
        case "mention":
            return "Someone mentioned you."
        case "repost", "post_reposted":
            return "Someone reposted your post."
        default:
            return "New notification."
        }
    }
}
