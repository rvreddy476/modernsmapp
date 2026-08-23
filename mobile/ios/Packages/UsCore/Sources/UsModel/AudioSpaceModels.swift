import Foundation

public struct Speaker: Identifiable, Hashable, Codable, Sendable {
    public let id: String
    public let name: String
    public let avatarUrl: String?
    public let isSpeaking: Bool
    public let isMuted: Bool
    public let isHost: Bool

    public init(
        id: String,
        name: String,
        avatarUrl: String? = nil,
        isSpeaking: Bool = false,
        isMuted: Bool = false,
        isHost: Bool = false
    ) {
        self.id = id
        self.name = name
        self.avatarUrl = avatarUrl
        self.isSpeaking = isSpeaking
        self.isMuted = isMuted
        self.isHost = isHost
    }
}

public struct AudioSpace: Identifiable, Hashable, Codable, Sendable {
    public let id: String
    public let title: String
    public let host: Speaker
    public let speakers: [Speaker]
    public let listenersCount: Int
    public let topic: String

    public init(
        id: String,
        title: String,
        host: Speaker,
        speakers: [Speaker] = [],
        listenersCount: Int = 240,
        topic: String = "Tech"
    ) {
        self.id = id
        self.title = title
        self.host = host
        self.speakers = speakers
        self.listenersCount = listenersCount
        self.topic = topic
    }
}
