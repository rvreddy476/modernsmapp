import Foundation

public struct BroadcastMessage: Identifiable, Hashable, Codable, Sendable {
    public let id: String
    public let text: String
    public let mediaUrl: String?
    public let voiceDurationSeconds: Int?
    public let reactionsCount: Int
    public let timestamp: String

    public init(
        id: String = UUID().uuidString,
        text: String,
        mediaUrl: String? = nil,
        voiceDurationSeconds: Int? = nil,
        reactionsCount: Int = 120,
        timestamp: String = "2h ago"
    ) {
        self.id = id
        self.text = text
        self.mediaUrl = mediaUrl
        self.voiceDurationSeconds = voiceDurationSeconds
        self.reactionsCount = reactionsCount
        self.timestamp = timestamp
    }
}

public struct BroadcastChannel: Identifiable, Hashable, Codable, Sendable {
    public let id: String
    public let name: String
    public let creator: Author
    public let membersCount: Int
    public let isJoined: Bool

    public init(
        id: String,
        name: String,
        creator: Author,
        membersCount: Int = 34500,
        isJoined: Bool = true
    ) {
        self.id = id
        self.name = name
        self.creator = creator
        self.membersCount = membersCount
        self.isJoined = isJoined
    }
}
