import Foundation

public struct ApiErrorEnvelope: Codable, Sendable, Equatable {
    public let code: String
    public let message: String
    public let verificationToken: String?

    enum CodingKeys: String, CodingKey {
        case code
        case message
        case verificationToken = "verification_token"
    }

    public init(code: String, message: String, verificationToken: String? = nil) {
        self.code = code
        self.message = message
        self.verificationToken = verificationToken
    }
}

public enum AppError: LocalizedError, Sendable, Equatable {
    case network(String)
    case unauthorized
    case forbidden
    case notFound
    case server(Int, String)
    case decoding(String)
    case api(code: String, message: String, verificationToken: String? = nil)
    case unknown

    public var errorDescription: String? {
        switch self {
        case .network(let message):
            return "Network connection issue: \(message)"
        case .unauthorized:
            return "Session expired. Please sign in again."
        case .forbidden:
            return "You do not have permission to view this."
        case .notFound:
            return "The requested content was not found."
        case .server(let code, let message):
            return "Server error (\(code)): \(message)"
        case .decoding(let message):
            return "Failed to process server response: \(message)"
        case .api(let code, let message, _):
            return "\(message) (\(code))"
        case .unknown:
            return "An unexpected error occurred."
        }
    }
}

public struct ApiMeta: Codable, Sendable, Equatable {
    public let nextCursor: String?
    public let hasMore: Bool?
    public let requestId: String?

    enum CodingKeys: String, CodingKey {
        case nextCursor = "next_cursor"
        case hasMore = "has_more"
        case requestId = "request_id"
    }

    public init(nextCursor: String? = nil, hasMore: Bool? = nil, requestId: String? = nil) {
        self.nextCursor = nextCursor
        self.hasMore = hasMore
        self.requestId = requestId
    }
}

public struct ApiEnvelope<T: Codable & Sendable>: Codable, Sendable {
    public let data: T?
    public let error: ApiErrorEnvelope?
    public let meta: ApiMeta?

    enum CodingKeys: String, CodingKey {
        case data
        case error
        case meta
    }

    public init(data: T? = nil, error: ApiErrorEnvelope? = nil, meta: ApiMeta? = nil) {
        self.data = data
        self.error = error
        self.meta = meta
    }
}

public struct EmptyData: Codable, Sendable, Equatable {
    public init() {}
}
