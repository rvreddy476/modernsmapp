import XCTest
@testable import UsNetwork
@testable import UsModel

final class MockKeyValueStorage: KeyValueStorage, @unchecked Sendable {
    private var dict: [String: String] = [:]

    func string(forKey key: String) -> String? {
        return dict[key]
    }

    @discardableResult
    func set(_ value: String?, forKey key: String) -> Bool {
        dict[key] = value
        return true
    }

    @discardableResult
    func remove(forKey key: String) -> Bool {
        dict.removeValue(forKey: key)
        return true
    }
}

final class APIClientTests: XCTestCase {
    func testSessionManagerSaveAndLoad() async {
        let storage = MockKeyValueStorage()
        let sessionManager = SessionManager(storage: storage)

        XCTAssertFalse(sessionManager.isAuthenticated)
        XCTAssertNil(await sessionManager.getAccessToken())

        let session = AuthSession(
            userId: "user-123",
            accessToken: "test-jwt-token",
            refreshToken: "test-refresh-token",
            expiresAt: "2026-08-24T00:00:00Z",
            username: "testuser",
            displayName: "Test User",
            email: "test@example.com"
        )

        await sessionManager.saveSession(session)

        XCTAssertTrue(sessionManager.isAuthenticated)
        let token = await sessionManager.getAccessToken()
        XCTAssertEqual(token, "test-jwt-token")
        XCTAssertEqual(sessionManager.currentSession?.username, "testuser")
        XCTAssertEqual(sessionManager.currentSession?.refreshToken, "test-refresh-token")

        await sessionManager.clearSession()
        XCTAssertFalse(sessionManager.isAuthenticated)
        let clearedToken = await sessionManager.getAccessToken()
        XCTAssertNil(clearedToken)
    }

    func testApiEnvelopeParsing() throws {
        let json = """
        {
            "data": [
                {
                    "id": "1",
                    "author_id": "auth-1",
                    "text": "Hello World",
                    "post_type": "text",
                    "created_at": "2026-08-21T00:00:00Z",
                    "updated_at": "2026-08-21T00:00:00Z"
                }
            ],
            "meta": {
                "next_cursor": "cursor_page_2",
                "has_more": true
            }
        }
        """.data(using: .utf8)!

        let envelope = try JSONDecoder().decode(ApiEnvelope<[FeedItem]>.self, from: json)
        XCTAssertEqual(envelope.data?.count, 1)
        XCTAssertEqual(envelope.data?.first?.text, "Hello World")
        XCTAssertEqual(envelope.meta?.nextCursor, "cursor_page_2")
        XCTAssertEqual(envelope.meta?.hasMore, true)
    }

    func testApiEnvelopeNullDataEmptyCase() throws {
        // Contract test: the platform omitempty returns {"data":null} for empty collections
        let emptyJson = """
        {
            "data": null
        }
        """.data(using: .utf8)!

        let envelope = try JSONDecoder().decode(ApiEnvelope<[NotificationItem]>.self, from: emptyJson)
        XCTAssertNil(envelope.data)
        let safeList = envelope.data ?? []
        XCTAssertTrue(safeList.isEmpty)
        XCTAssertNil(envelope.error)
    }

    func testApiEnvelopeErrorPayloadWithVerificationToken() throws {
        let errorJson = """
        {
            "error": {
                "code": "EMAIL_NOT_VERIFIED",
                "message": "Email address requires verification before login",
                "verification_token": "verify_tok_998877"
            }
        }
        """.data(using: .utf8)!

        let envelope = try JSONDecoder().decode(ApiEnvelope<EmptyData>.self, from: errorJson)
        XCTAssertNil(envelope.data)
        XCTAssertNotNil(envelope.error)
        XCTAssertEqual(envelope.error?.code, "EMAIL_NOT_VERIFIED")
        XCTAssertEqual(envelope.error?.verificationToken, "verify_tok_998877")
    }

    func testLoginResponseShapeDecoding() throws {
        let liveLoginJson = """
        {
            "data": {
                "tokens": {
                    "access_token": "jwt_access_abc_123",
                    "refresh_token": "ref_tok_def_456",
                    "expires_at": "2026-08-24T00:35:20.03808468Z"
                },
                "user": {
                    "id": "51640494-ecb9-4856-9b09-7542ff2823e1",
                    "email": "ios_test@example.com",
                    "email_verified": true,
                    "phone_verified": false,
                    "account_type": "personal",
                    "account_status": "active"
                },
                "session_id": "bac8ad6a-78b8-4e58-bcac-c53a2d8bdca2"
            }
        }
        """.data(using: .utf8)!

        struct LoginTokens: Codable {
            let accessToken: String
            let refreshToken: String?
            let expiresAt: String?
            enum CodingKeys: String, CodingKey {
                case accessToken = "access_token"
                case refreshToken = "refresh_token"
                case expiresAt = "expires_at"
            }
        }
        struct LoginUser: Codable {
            let id: String
            let email: String?
            let emailVerified: Bool?
            enum CodingKeys: String, CodingKey {
                case id
                case email
                case emailVerified = "email_verified"
            }
        }
        struct LoginPayload: Codable {
            let tokens: LoginTokens
            let user: LoginUser
            let sessionId: String?
            enum CodingKeys: String, CodingKey {
                case tokens
                case user
                case sessionId = "session_id"
            }
        }

        let envelope = try JSONDecoder().decode(ApiEnvelope<LoginPayload>.self, from: liveLoginJson)
        guard let data = envelope.data else {
            XCTFail("Expected non-nil login data")
            return
        }

        XCTAssertEqual(data.tokens.accessToken, "jwt_access_abc_123")
        XCTAssertEqual(data.tokens.refreshToken, "ref_tok_def_456")
        XCTAssertEqual(data.user.id, "51640494-ecb9-4856-9b09-7542ff2823e1")
        XCTAssertEqual(data.user.email, "ios_test@example.com")
        XCTAssertEqual(data.user.emailVerified, true)
        XCTAssertEqual(data.sessionId, "bac8ad6a-78b8-4e58-bcac-c53a2d8bdca2")
    }

    func testMediaStatusReadinessGateExactReadyAndPassed() {
        struct StatusCheck {
            let processingStatus: String
            let moderationStatus: String?
            var isReadyAndPassed: Bool {
                return processingStatus == "ready" && moderationStatus == "passed"
            }
        }

        let readyPassed = StatusCheck(processingStatus: "ready", moderationStatus: "passed")
        XCTAssertTrue(readyPassed.isReadyAndPassed)

        let uploadedPending = StatusCheck(processingStatus: "uploaded", moderationStatus: "pending")
        XCTAssertFalse(uploadedPending.isReadyAndPassed)

        let readyPending = StatusCheck(processingStatus: "ready", moderationStatus: "pending")
        XCTAssertFalse(readyPending.isReadyAndPassed)

        let processingPassed = StatusCheck(processingStatus: "processing", moderationStatus: "passed")
        XCTAssertFalse(processingPassed.isReadyAndPassed)

        let failedRejected = StatusCheck(processingStatus: "failed", moderationStatus: "rejected")
        XCTAssertFalse(failedRejected.isReadyAndPassed)
    }
}
