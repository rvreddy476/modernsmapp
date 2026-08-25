import Foundation

public struct AudioTrack: Identifiable, Hashable, Codable, Sendable {
    public let id: String
    public let title: String
    public let artist: String
    public let duration: Double
    public let previewUrl: String?
    public let coverUrl: String?
    public let usageCount: Int

    public init(
        id: String,
        title: String,
        artist: String,
        duration: Double = 30.0,
        previewUrl: String? = nil,
        coverUrl: String? = nil,
        usageCount: Int = 0
    ) {
        self.id = id
        self.title = title
        self.artist = artist
        self.duration = duration
        self.previewUrl = previewUrl
        self.coverUrl = coverUrl
        self.usageCount = usageCount
    }

    enum CodingKeys: String, CodingKey {
        case id
        case title
        case artist
        case duration
        case previewUrl = "preview_url"
        case coverUrl = "cover_url"
        case usageCount = "usage_count"
    }
}
