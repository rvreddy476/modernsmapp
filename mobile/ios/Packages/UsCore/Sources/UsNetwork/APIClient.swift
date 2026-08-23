import Foundation
import UsModel

public enum AppEnvironment: String, Sendable, CaseIterable {
    case development
    case staging
    case production

    public var baseURL: URL {
        switch self {
        case .development:
            return URL(string: "http://127.0.0.1:8080")!
        case .staging:
            return URL(string: "https://staging-api.us.com")!
        case .production:
            return URL(string: "https://api.us.com")!
        }
    }
}

public struct ApiConfig: Sendable {
    public static var environment: AppEnvironment = .development
    public static var currentBaseURL: URL {
        return environment.baseURL
    }
}

public protocol AuthTokenProvider: Sendable {
    func getAccessToken() async -> String?
    func refreshAccessToken() async throws -> String
}

public final class DefaultAuthTokenProvider: AuthTokenProvider, @unchecked Sendable {
    private var token: String?

    public init(initialToken: String? = nil) {
        self.token = initialToken
    }

    public func setToken(_ token: String?) {
        self.token = token
    }

    public func getAccessToken() async -> String? {
        return token
    }

    public func refreshAccessToken() async throws -> String {
        guard let current = token else { throw AppError.unauthorized }
        return current
    }
}

public protocol APIClientProtocol: Sendable {
    func request<T: Codable & Sendable>(
        endpoint: String,
        method: String,
        query: [String: String]?,
        headers: [String: String]?,
        body: Data?
    ) async throws -> T

    func requestEnvelope<T: Codable & Sendable>(
        endpoint: String,
        method: String,
        query: [String: String]?,
        headers: [String: String]?,
        body: Data?
    ) async throws -> ApiEnvelope<T>
}

public extension APIClientProtocol {
    func request<T: Codable & Sendable>(
        endpoint: String,
        method: String = "GET",
        query: [String: String]? = nil,
        body: Data? = nil
    ) async throws -> T {
        return try await request(endpoint: endpoint, method: method, query: query, headers: nil, body: body)
    }

    func requestEnvelope<T: Codable & Sendable>(
        endpoint: String,
        method: String = "GET",
        query: [String: String]? = nil,
        body: Data? = nil
    ) async throws -> ApiEnvelope<T> {
        return try await requestEnvelope(endpoint: endpoint, method: method, query: query, headers: nil, body: body)
    }
}

public final class APIClient: APIClientProtocol, Sendable {
    private let baseURLProvider: @Sendable () -> URL
    private let session: URLSession
    private let authProvider: AuthTokenProvider?

    public init(
        baseURL: URL? = nil,
        session: URLSession = .shared,
        authProvider: AuthTokenProvider? = SessionManager.shared
    ) {
        if let explicitURL = baseURL {
            self.baseURLProvider = { explicitURL }
        } else {
            self.baseURLProvider = { ApiConfig.currentBaseURL }
        }
        self.session = session
        self.authProvider = authProvider
    }

    public func request<T: Codable & Sendable>(
        endpoint: String,
        method: String = "GET",
        query: [String: String]? = nil,
        headers: [String: String]? = nil,
        body: Data? = nil
    ) async throws -> T {
        let envelope: ApiEnvelope<T> = try await requestEnvelope(
            endpoint: endpoint,
            method: method,
            query: query,
            headers: headers,
            body: body
        )
        if let data = envelope.data {
            return data
        }
        if let empty = EmptyData() as? T {
            return empty
        }
        if let error = envelope.error {
            throw AppError.api(code: error.code, message: error.message, verificationToken: error.verificationToken)
        }
        throw AppError.notFound
    }

    public func requestEnvelope<T: Codable & Sendable>(
        endpoint: String,
        method: String = "GET",
        query: [String: String]? = nil,
        headers: [String: String]? = nil,
        body: Data? = nil
    ) async throws -> ApiEnvelope<T> {
        return try await performRequestEnvelope(
            endpoint: endpoint,
            method: method,
            query: query,
            headers: headers,
            body: body,
            allowRetryOn401: true
        )
    }

    private func performRequestEnvelope<T: Codable & Sendable>(
        endpoint: String,
        method: String,
        query: [String: String]?,
        headers: [String: String]?,
        body: Data?,
        allowRetryOn401: Bool
    ) async throws -> ApiEnvelope<T> {
        let baseURL = baseURLProvider()
        let cleanEndpoint = endpoint.hasPrefix("/") ? String(endpoint.dropFirst()) : endpoint
        var components = URLComponents(url: baseURL.appendingPathComponent(cleanEndpoint), resolvingAgainstBaseURL: true)
        if let query = query, !query.isEmpty {
            components?.queryItems = query.map { URLQueryItem(name: $0.key, value: $0.value) }
        }

        guard let requestURL = components?.url else {
            throw AppError.network("Malformed URL for endpoint: \(endpoint)")
        }

        var urlRequest = URLRequest(url: requestURL)
        urlRequest.httpMethod = method
        urlRequest.setValue("application/json", forHTTPHeaderField: "Accept")

        if let customHeaders = headers {
            for (key, value) in customHeaders {
                urlRequest.setValue(value, forHTTPHeaderField: key)
            }
        }

        if let token = await authProvider?.getAccessToken(), !token.isEmpty {
            urlRequest.setValue("Bearer \(token)", forHTTPHeaderField: "Authorization")
        }

        if let body = body {
            urlRequest.httpBody = body
            if urlRequest.value(forHTTPHeaderField: "Content-Type") == nil {
                urlRequest.setValue("application/json", forHTTPHeaderField: "Content-Type")
            }
        }

        let data: Data
        let response: URLResponse
        do {
            (data, response) = try await session.data(for: urlRequest)
        } catch {
            throw AppError.network(error.localizedDescription)
        }

        guard let httpResponse = response as? HTTPURLResponse else {
            throw AppError.unknown
        }

        // Handle 401 with Token Refresh Retry
        if httpResponse.statusCode == 401 && allowRetryOn401, let auth = authProvider {
            do {
                _ = try await auth.refreshAccessToken()
                return try await performRequestEnvelope(
                    endpoint: endpoint,
                    method: method,
                    query: query,
                    headers: headers,
                    body: body,
                    allowRetryOn401: false
                )
            } catch {
                throw AppError.unauthorized
            }
        }

        switch httpResponse.statusCode {
        case 200...299:
            do {
                let decoder = JSONDecoder()
                return try decoder.decode(ApiEnvelope<T>.self, from: data)
            } catch {
                // If not wrapped in standard envelope, attempt bare decode
                do {
                    let decoder = JSONDecoder()
                    let bareData = try decoder.decode(T.self, from: data)
                    return ApiEnvelope(data: bareData, meta: nil)
                } catch {
                    throw AppError.decoding(error.localizedDescription)
                }
            }
        case 401:
            throw AppError.unauthorized
        case 403:
            if let errorEnvelope = try? JSONDecoder().decode(ApiEnvelope<EmptyData>.self, from: data),
               let err = errorEnvelope.error {
                throw AppError.api(code: err.code, message: err.message, verificationToken: err.verificationToken)
            }
            throw AppError.forbidden
        case 404:
            if let errorEnvelope = try? JSONDecoder().decode(ApiEnvelope<EmptyData>.self, from: data),
               let err = errorEnvelope.error {
                throw AppError.api(code: err.code, message: err.message, verificationToken: err.verificationToken)
            }
            throw AppError.notFound
        default:
            if let errorEnvelope = try? JSONDecoder().decode(ApiEnvelope<EmptyData>.self, from: data),
               let err = errorEnvelope.error {
                throw AppError.api(code: err.code, message: err.message, verificationToken: err.verificationToken)
            }
            let msg = String(data: data, encoding: .utf8) ?? "HTTP \(httpResponse.statusCode)"
            throw AppError.server(httpResponse.statusCode, msg)
        }
    }
}
