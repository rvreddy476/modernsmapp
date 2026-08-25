import SwiftUI
import UsModel
import UsDesignSystem
import UsNetwork

public struct LoginResponseData: Codable, Sendable {
    public struct Tokens: Codable, Sendable {
        public let accessToken: String
        public let refreshToken: String?
        public let expiresAt: String?

        enum CodingKeys: String, CodingKey {
            case accessToken = "access_token"
            case refreshToken = "refresh_token"
            case expiresAt = "expires_at"
        }

        public init(accessToken: String, refreshToken: String? = nil, expiresAt: String? = nil) {
            self.accessToken = accessToken
            self.refreshToken = refreshToken
            self.expiresAt = expiresAt
        }
    }

    public struct User: Codable, Sendable {
        public let id: String
        public let email: String?
        public let emailVerified: Bool?

        enum CodingKeys: String, CodingKey {
            case id
            case email
            case emailVerified = "email_verified"
        }

        public init(id: String, email: String? = nil, emailVerified: Bool? = nil) {
            self.id = id
            self.email = email
            self.emailVerified = emailVerified
        }
    }

    public let tokens: Tokens
    public let user: User
    public let sessionId: String?
    public let requiresVerification: Bool?
    public let verificationToken: String?

    enum CodingKeys: String, CodingKey {
        case tokens
        case user
        case sessionId = "session_id"
        case requiresVerification = "requires_verification"
        case verificationToken = "verification_token"
    }

    public init(tokens: Tokens, user: User, sessionId: String? = nil, requiresVerification: Bool? = nil, verificationToken: String? = nil) {
        self.tokens = tokens
        self.user = user
        self.sessionId = sessionId
        self.requiresVerification = requiresVerification
        self.verificationToken = verificationToken
    }
}

public struct RegisterResponseData: Codable, Sendable {
    public struct Tokens: Codable, Sendable {
        public let accessToken: String?
        public let refreshToken: String?
        public let expiresAt: String?

        enum CodingKeys: String, CodingKey {
            case accessToken = "access_token"
            case refreshToken = "refresh_token"
            case expiresAt = "expires_at"
        }
    }

    public struct User: Codable, Sendable {
        public let id: String
        public let email: String?
    }

    public let tokens: Tokens?
    public let user: User?
    public let sessionId: String?
    public let requiresVerification: Bool?
    public let verificationToken: String?

    enum CodingKeys: String, CodingKey {
        case tokens
        case user
        case sessionId = "session_id"
        case requiresVerification = "requires_verification"
        case verificationToken = "verification_token"
    }
}

public struct VerifyEmailResponseData: Codable, Sendable {
    public let message: String?
}

@Observable
public final class AuthViewModel: @unchecked Sendable {
    public var identifier: String = "" // email or username
    public var password: String = ""
    public var username: String = ""
    public var displayName: String = ""
    public var firstName: String = ""
    public var lastName: String = ""
    public var dob: String = "1998-05-15"
    public var gender: String = "other" // male, female, other
    public var acceptedTerms: Bool = true
    public var termsVersion: String = "2026-08-01"
    public var otpCode: String = ""
    public var verificationToken: String? = nil

    public var isLoading: Bool = false
    public var errorMessage: String? = nil
    public var successMessage: String? = nil
    public var needsOTP: Bool = false

    private let client: APIClientProtocol
    private let sessionManager: SessionManager

    public init(
        client: APIClientProtocol = APIClient(),
        sessionManager: SessionManager = .shared
    ) {
        self.client = client
        self.sessionManager = sessionManager
    }

    @MainActor
    public func login() async {
        let cleanId = identifier.trimmingCharacters(in: .whitespacesAndNewlines)
        guard !cleanId.isEmpty, !password.isEmpty else {
            errorMessage = "Please enter both credentials."
            return
        }

        isLoading = true
        errorMessage = nil
        successMessage = nil
        do {
            let body = try JSONEncoder().encode([
                "identifier": cleanId,
                "password": password
            ])

            let response: LoginResponseData = try await client.request(
                endpoint: "v1/auth/login",
                method: "POST",
                query: nil,
                body: body
            )

            if response.requiresVerification == true || response.tokens.accessToken.isEmpty {
                self.verificationToken = response.verificationToken
                self.needsOTP = true
            } else {
                let session = AuthSession(
                    userId: response.user.id,
                    accessToken: response.tokens.accessToken,
                    refreshToken: response.tokens.refreshToken,
                    expiresAt: response.tokens.expiresAt,
                    username: cleanId,
                    displayName: displayName.isEmpty ? nil : displayName,
                    email: response.user.email
                )
                sessionManager.saveSession(session)
            }
        } catch let appError as AppError {
            if case .api(let code, let msg, let token) = appError {
                if code == "EMAIL_NOT_VERIFIED", let token = token {
                    self.verificationToken = token
                    self.needsOTP = true
                    self.errorMessage = "Please verify your email address."
                } else {
                    self.errorMessage = "\(msg) (\(code))"
                }
            } else {
                self.errorMessage = appError.localizedDescription
            }
        } catch {
            self.errorMessage = error.localizedDescription
        }
        self.isLoading = false
    }

    @MainActor
    public func register() async {
        let cleanUser = username.trimmingCharacters(in: .whitespacesAndNewlines)
        let cleanEmail = identifier.trimmingCharacters(in: .whitespacesAndNewlines)
        let cleanFirst = firstName.trimmingCharacters(in: .whitespacesAndNewlines)
        let cleanLast = lastName.trimmingCharacters(in: .whitespacesAndNewlines)
        let first = cleanFirst.isEmpty ? "User" : cleanFirst
        let last = cleanLast.isEmpty ? "Member" : cleanLast

        guard !cleanUser.isEmpty, !cleanEmail.isEmpty, !password.isEmpty else {
            errorMessage = "Please fill in all required fields."
            return
        }

        isLoading = true
        errorMessage = nil
        successMessage = nil
        do {
            struct RegisterPayload: Codable {
                let username: String
                let email: String
                let password: String
                let displayName: String
                let firstName: String
                let lastName: String
                let dob: String
                let gender: String
                let acceptedTerms: Bool
                let termsVersion: String

                enum CodingKeys: String, CodingKey {
                    case username
                    case email
                    case password
                    case displayName = "display_name"
                    case firstName = "first_name"
                    case lastName = "last_name"
                    case dob
                    case gender
                    case acceptedTerms = "accepted_terms"
                    case termsVersion = "terms_version"
                }
            }

            let payload = RegisterPayload(
                username: cleanUser,
                email: cleanEmail,
                password: password,
                displayName: displayName.isEmpty ? cleanUser : displayName,
                firstName: first,
                lastName: last,
                dob: dob.isEmpty ? "1998-05-15" : dob,
                gender: gender,
                acceptedTerms: acceptedTerms,
                termsVersion: termsVersion
            )

            let body = try JSONEncoder().encode(payload)
            let response: RegisterResponseData = try await client.request(
                endpoint: "v1/auth/register",
                method: "POST",
                query: nil,
                body: body
            )

            if let vToken = response.verificationToken {
                self.verificationToken = vToken
                self.needsOTP = true
            } else if let tokens = response.tokens, let token = tokens.accessToken, !token.isEmpty, let user = response.user {
                let session = AuthSession(
                    userId: user.id,
                    accessToken: token,
                    refreshToken: tokens.refreshToken,
                    expiresAt: tokens.expiresAt,
                    username: cleanUser,
                    displayName: displayName,
                    email: cleanEmail
                )
                sessionManager.saveSession(session)
            } else {
                self.needsOTP = true
            }
        } catch let appError as AppError {
            self.errorMessage = appError.localizedDescription
        } catch {
            self.errorMessage = error.localizedDescription
        }
        self.isLoading = false
    }

    @MainActor
    public func verifyEmail() async {
        let cleanCode = otpCode.trimmingCharacters(in: .whitespacesAndNewlines)
        guard let token = verificationToken, !token.isEmpty, !cleanCode.isEmpty else {
            errorMessage = "Please enter the verification code."
            return
        }

        isLoading = true
        errorMessage = nil
        successMessage = nil
        do {
            let body = try JSONEncoder().encode([
                "verification_token": token,
                "code": cleanCode
            ])

            let _: VerifyEmailResponseData = try await client.request(
                endpoint: "v1/auth/verify-email",
                method: "POST",
                query: nil,
                body: body
            )

            self.successMessage = "Email verified! Signing you in..."
            self.needsOTP = false

            // Automatically login after verification if password is known
            if !password.isEmpty && !identifier.isEmpty {
                await login()
            }
        } catch let appError as AppError {
            self.errorMessage = appError.localizedDescription
        } catch {
            self.errorMessage = error.localizedDescription
        }
        self.isLoading = false
    }
}
