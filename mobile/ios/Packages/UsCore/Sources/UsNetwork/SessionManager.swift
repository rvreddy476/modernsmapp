import Foundation
import UsModel

public struct AuthSession: Codable, Sendable, Equatable {
    public let userId: String
    public let accessToken: String
    public let refreshToken: String?
    public let expiresAt: String?
    public let username: String?
    public let displayName: String?
    public let email: String?

    public init(
        userId: String,
        accessToken: String,
        refreshToken: String? = nil,
        expiresAt: String? = nil,
        username: String? = nil,
        displayName: String? = nil,
        email: String? = nil
    ) {
        self.userId = userId
        self.accessToken = accessToken
        self.refreshToken = refreshToken
        self.expiresAt = expiresAt
        self.username = username
        self.displayName = displayName
        self.email = email
    }
}

private struct RefreshTokensResponse: Codable {
    struct TokenData: Codable {
        let accessToken: String
        let refreshToken: String?
        let expiresAt: String?

        enum CodingKeys: String, CodingKey {
            case accessToken = "access_token"
            case refreshToken = "refresh_token"
            case expiresAt = "expires_at"
        }
    }
    struct UserData: Codable {
        let id: String
        let email: String?
    }
    let tokens: TokenData
    let user: UserData?
    let sessionId: String?

    enum CodingKeys: String, CodingKey {
        case tokens
        case user
        case sessionId = "session_id"
    }
}

@Observable
public final class SessionManager: AuthTokenProvider, @unchecked Sendable {
    public static let shared = SessionManager()

    public private(set) var currentSession: AuthSession?
    public var isAuthenticated: Bool { currentSession != nil }

    private let storage: KeyValueStorage
    private let sessionKey = "us_auth_session_v1"
    private let lock = NSLock()
    private var inFlightRefreshTask: Task<String, Error>?

    public init(storage: KeyValueStorage = KeychainStorage()) {
        self.storage = storage
        loadSession()
    }

    private func loadSession() {
        if let dataString = storage.string(forKey: sessionKey),
           let data = dataString.data(using: .utf8),
           let session = try? JSONDecoder().decode(AuthSession.self, from: data) {
            self.currentSession = session
        }
    }

    @MainActor
    public func saveSession(_ session: AuthSession) {
        self.currentSession = session
        if let data = try? JSONEncoder().encode(session),
           let str = String(data: data, encoding: .utf8) {
            storage.set(str, forKey: sessionKey)
        }
    }

    @MainActor
    public func clearSession() {
        self.currentSession = nil
        storage.remove(forKey: sessionKey)
    }

    public func getAccessToken() async -> String? {
        lock.lock()
        defer { lock.unlock() }
        return currentSession?.accessToken
    }

    public func refreshAccessToken() async throws -> String {
        lock.lock()
        // If a refresh is already in progress, join the existing task to collapse concurrent 401s
        if let existingTask = inFlightRefreshTask {
            lock.unlock()
            return try await existingTask.value
        }

        guard let current = currentSession, let refreshToken = current.refreshToken, !refreshToken.isEmpty else {
            lock.unlock()
            throw AppError.unauthorized
        }

        let task = Task<String, Error> {
            do {
                let refreshURL = ApiConfig.currentBaseURL.appendingPathComponent("v1/auth/refresh")
                var request = URLRequest(url: refreshURL)
                request.httpMethod = "POST"
                request.setValue("application/json", forHTTPHeaderField: "Content-Type")
                request.setValue("application/json", forHTTPHeaderField: "Accept")

                let payload = ["refresh_token": refreshToken]
                request.httpBody = try JSONEncoder().encode(payload)

                let (data, response) = try await URLSession.shared.data(for: request)
                guard let httpResponse = response as? HTTPURLResponse else {
                    throw AppError.unknown
                }

                if httpResponse.statusCode == 200 {
                    let envelope = try JSONDecoder().decode(ApiEnvelope<RefreshTokensResponse>.self, from: data)
                    guard let responseData = envelope.data else {
                        throw AppError.unauthorized
                    }

                    let newAccessToken = responseData.tokens.accessToken
                    let newRefreshToken = responseData.tokens.refreshToken ?? refreshToken
                    let newExpiresAt = responseData.tokens.expiresAt

                    let updatedSession = AuthSession(
                        userId: current.userId,
                        accessToken: newAccessToken,
                        refreshToken: newRefreshToken,
                        expiresAt: newExpiresAt,
                        username: current.username,
                        displayName: current.displayName,
                        email: current.email
                    )

                    await MainActor.run {
                        self.saveSession(updatedSession)
                    }

                    self.lock.lock()
                    self.inFlightRefreshTask = nil
                    self.lock.unlock()

                    return newAccessToken
                } else {
                    await MainActor.run {
                        self.clearSession()
                    }
                    self.lock.lock()
                    self.inFlightRefreshTask = nil
                    self.lock.unlock()
                    throw AppError.unauthorized
                }
            } catch {
                self.lock.lock()
                self.inFlightRefreshTask = nil
                self.lock.unlock()
                throw error
            }
        }

        self.inFlightRefreshTask = task
        lock.unlock()

        return try await task.value
    }
}
