import Foundation

public struct MediaVariant: Hashable, Codable, Sendable {
    public let original: String?
    public let thumb150: String?
    public let p360: String?
    public let p720: String?
    public let p1080: String?

    public init(
        original: String? = nil,
        thumb150: String? = nil,
        p360: String? = nil,
        p720: String? = nil,
        p1080: String? = nil
    ) {
        self.original = original
        self.thumb150 = thumb150
        self.p360 = p360
        self.p720 = p720
        self.p1080 = p1080
    }

    enum CodingKeys: String, CodingKey {
        case original
        case thumb150 = "thumb_150"
        case p360 = "360p"
        case p720 = "720p"
        case p1080 = "1080p"
    }
}

public struct FeedMedia: Identifiable, Hashable, Codable, Sendable {
    public let mediaId: String
    public let kind: String
    public let status: String
    public let width: Int?
    public let height: Int?
    public let blurhash: String?
    public let variants: MediaVariant?
    public let hlsUrl: String?
    public let expiresAt: String?

    public var id: String { mediaId }

    public init(
        mediaId: String,
        kind: String,
        status: String,
        width: Int? = nil,
        height: Int? = nil,
        blurhash: String? = nil,
        variants: MediaVariant? = nil,
        hlsUrl: String? = nil,
        expiresAt: String? = nil
    ) {
        self.mediaId = mediaId
        self.kind = kind
        self.status = status
        self.width = width
        self.height = height
        self.blurhash = blurhash
        self.variants = variants
        self.hlsUrl = hlsUrl
        self.expiresAt = expiresAt
    }

    public var isReady: Bool {
        status == "ready"
    }

    public var isVideo: Bool {
        kind == "video" || kind == "flick" || kind == "long_video"
    }

    public var posterUrl: String? {
        variants?.thumb150 ?? variants?.original
    }

    public var videoStreamUrl: String? {
        hlsUrl ?? variants?.p720 ?? variants?.p360 ?? variants?.original
    }

    public var aspectRatio: Float {
        guard let w = width, let h = height, h > 0 else {
            return 16.0 / 9.0
        }
        return Float(w) / Float(h)
    }

    enum CodingKeys: String, CodingKey {
        case mediaId = "media_id"
        case kind
        case status
        case width
        case height
        case blurhash
        case variants
        case hlsUrl = "hls_url"
        case expiresAt = "expires_at"
    }
}
