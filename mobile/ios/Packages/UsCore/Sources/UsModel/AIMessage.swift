import Foundation

public struct AIMessage: Identifiable, Hashable, Codable, Sendable {
    public let id: String
    public let role: String // "user" or "assistant"
    public let content: String
    public let timestamp: String

    public init(id: String = UUID().uuidString, role: String, content: String, timestamp: String = "Now") {
        self.id = id
        self.role = role
        self.content = content
        self.timestamp = timestamp
    }
}
