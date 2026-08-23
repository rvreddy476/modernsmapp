import Foundation
import UsModel
import UsNetwork

public struct MediaInitResponse: Codable, Sendable {
    public let mediaId: String
    public let uploadUrl: String
    public let objectKey: String?
    public let expiresAt: String?

    enum CodingKeys: String, CodingKey {
        case mediaId = "media_id"
        case uploadUrl = "upload_url"
        case objectKey = "object_key"
        case expiresAt = "expires_at"
    }

    public init(mediaId: String, uploadUrl: String, objectKey: String? = nil, expiresAt: String? = nil) {
        self.mediaId = mediaId
        self.uploadUrl = uploadUrl
        self.objectKey = objectKey
        self.expiresAt = expiresAt
    }
}

public struct MediaStatusResponse: Codable, Sendable {
    public let mediaId: String
    public let processingStatus: String
    public let moderationStatus: String?
    public let fileType: String?

    enum CodingKeys: String, CodingKey {
        case mediaId = "media_id"
        case processingStatus = "processing_status"
        case moderationStatus = "moderation_status"
        case fileType = "file_type"
    }

    public init(mediaId: String, processingStatus: String, moderationStatus: String? = nil, fileType: String? = nil) {
        self.mediaId = mediaId
        self.processingStatus = processingStatus
        self.moderationStatus = moderationStatus
        self.fileType = fileType
    }

    public var isReadyAndPassed: Bool {
        return processingStatus == "ready" && moderationStatus == "passed"
    }
}

public protocol MediaUploaderProtocol: Sendable {
    func uploadImage(
        data: Data,
        mimeType: String,
        altText: String,
        decorative: Bool,
        uploadPurpose: String,
        progressHandler: (@Sendable (Double) -> Void)?
    ) async throws -> String
}

public final class MediaUploader: NSObject, MediaUploaderProtocol, URLSessionTaskDelegate, @unchecked Sendable {
    private let apiClient: APIClientProtocol
    private let rawSession: URLSession
    private var progressCallbacks: [Int: (@Sendable (Double) -> Void)] = [:]
    private let lock = NSLock()

    public init(
        apiClient: APIClientProtocol = APIClient(),
        rawSession: URLSession = .shared
    ) {
        self.apiClient = apiClient
        self.rawSession = rawSession
        super.init()
    }

    public func uploadImage(
        data: Data,
        mimeType: String = "image/jpeg",
        altText: String = "",
        decorative: Bool = false,
        uploadPurpose: String = "composer",
        progressHandler: (@Sendable (Double) -> Void)? = nil
    ) async throws -> String {
        // Step 1: Media Init
        struct MediaInitPayload: Codable {
            let fileType: String
            let mediaSubtype: String
            let mimeType: String
            let fileSizeBytes: Int
            let altText: String
            let decorative: Bool
            let uploadPurpose: String

            enum CodingKeys: String, CodingKey {
                case fileType = "file_type"
                case mediaSubtype = "media_subtype"
                case mimeType = "mime_type"
                case fileSizeBytes = "file_size_bytes"
                case altText = "alt_text"
                case decorative
                case uploadPurpose = "upload_purpose"
            }
        }

        let initPayload = MediaInitPayload(
            fileType: "image",
            mediaSubtype: "general",
            mimeType: mimeType,
            fileSizeBytes: data.count,
            altText: altText,
            decorative: decorative,
            uploadPurpose: uploadPurpose
        )

        let initBody = try JSONEncoder().encode(initPayload)
        let initResponse: MediaInitResponse = try await apiClient.request(
            endpoint: "v1/media/init",
            method: "POST",
            query: nil,
            body: initBody
        )

        let mediaId = initResponse.mediaId
        guard let uploadURL = URL(string: initResponse.uploadUrl) else {
            throw AppError.network("Invalid presigned upload URL")
        }

        // Step 2: Presigned PUT (NO Authorization header — the signature IS the auth)
        var putRequest = URLRequest(url: uploadURL)
        putRequest.httpMethod = "PUT"
        putRequest.setValue(mimeType, forHTTPHeaderField: "Content-Type")
        putRequest.httpBody = data

        let delegateSession = URLSession(configuration: .default, delegate: self, delegateQueue: nil)
        let uploadTask = delegateSession.uploadTask(with: putRequest, from: data)

        if let handler = progressHandler {
            lock.lock()
            progressCallbacks[uploadTask.taskIdentifier] = handler
            lock.unlock()
        }

        let (_, putResponse) = try await withCheckedThrowingContinuation { (continuation: CheckedContinuation<(Data, URLResponse), Error>) in
            let task = delegateSession.dataTask(with: putRequest) { respData, response, error in
                if let error = error {
                    continuation.resume(throwing: error)
                } else if let response = response {
                    continuation.resume(returning: (respData ?? Data(), response))
                } else {
                    continuation.resume(throwing: AppError.unknown)
                }
            }
            task.resume()
        }

        guard let httpPutResponse = putResponse as? HTTPURLResponse, (200...299).contains(httpPutResponse.statusCode) else {
            throw AppError.server((putResponse as? HTTPURLResponse)?.statusCode ?? 500, "Presigned PUT failed")
        }

        // Step 3: Confirm Upload
        struct ConfirmPayload: Codable {
            let mediaId: String
            enum CodingKeys: String, CodingKey {
                case mediaId = "media_id"
            }
        }
        let confirmBody = try JSONEncoder().encode(ConfirmPayload(mediaId: mediaId))
        let _: EmptyData = try await apiClient.request(
            endpoint: "v1/media/confirm",
            method: "POST",
            query: nil,
            body: confirmBody
        )

        // Step 4: Poll Status until ready AND passed
        var isReady = false
        var attempts = 0
        while !isReady && attempts < 15 {
            attempts += 1
            try await Task.sleep(nanoseconds: 500_000_000) // 0.5s

            let status: MediaStatusResponse = try await apiClient.request(
                endpoint: "v1/media/\(mediaId)/status",
                method: "GET",
                query: nil,
                body: nil
            )

            if status.processingStatus == "rejected" || status.processingStatus == "failed" {
                throw AppError.api(code: "MEDIA_REJECTED", message: "Media processing failed or was rejected")
            }

            if status.isReadyAndPassed {
                isReady = true
                break
            }
        }

        guard isReady else {
            throw AppError.api(code: "MEDIA_TIMEOUT", message: "Media processing timed out waiting for ready+passed state")
        }

        // Step 5: Patch alt-text
        if !altText.isEmpty || decorative {
            struct AltTextPayload: Codable {
                let altText: String
                let decorative: Bool
                enum CodingKeys: String, CodingKey {
                    case altText = "alt_text"
                    case decorative
                }
            }
            let altBody = try JSONEncoder().encode(AltTextPayload(altText: altText, decorative: decorative))
            let _: EmptyData = try await apiClient.request(
                endpoint: "v1/media/\(mediaId)/alt-text",
                method: "PATCH",
                query: nil,
                body: altBody
            )
        }

        return mediaId
    }

    public func urlSession(
        _ session: URLSession,
        task: URLSessionTask,
        didSendBodyData bytesSent: Int64,
        totalBytesSent: Int64,
        totalBytesExpectedToSend: Int64
    ) {
        guard totalBytesExpectedToSend > 0 else { return }
        let progress = Double(totalBytesSent) / Double(totalBytesExpectedToSend)
        lock.lock()
        let handler = progressCallbacks[task.taskIdentifier]
        lock.unlock()
        handler?(progress)
    }
}
