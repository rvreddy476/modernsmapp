import Foundation
import UsModel

public struct ComposerDraft: Codable, Equatable, Sendable {
    public let idempotencyKey: String
    public let text: String
    public let confirmedMediaId: String?
    public let altText: String
    public let isDecorative: Bool
    public let language: String
    public let selectedImageData: Data?
    public let selectedImageMimeType: String
    public let updatedAt: Date

    public init(
        idempotencyKey: String,
        text: String,
        confirmedMediaId: String? = nil,
        altText: String = "",
        isDecorative: Bool = false,
        language: String = "en",
        selectedImageData: Data? = nil,
        selectedImageMimeType: String = "image/jpeg",
        updatedAt: Date = Date()
    ) {
        self.idempotencyKey = idempotencyKey
        self.text = text
        self.confirmedMediaId = confirmedMediaId
        self.altText = altText
        self.isDecorative = isDecorative
        self.language = language
        self.selectedImageData = selectedImageData
        self.selectedImageMimeType = selectedImageMimeType
        self.updatedAt = updatedAt
    }

    /// Restores the frozen key ONLY if the request payload was also preserved.
    /// Half a frozen operation is worse than none: a key without its bytes forces
    /// a rebuilt request, and a rebuild that differs is refused by the server as key reuse.
    public var isValidForRestoration: Bool {
        let cleanKey = idempotencyKey.trimmingCharacters(in: .whitespacesAndNewlines)
        guard !cleanKey.isEmpty else { return false }

        let cleanText = text.trimmingCharacters(in: .whitespacesAndNewlines)
        let hasContent = !cleanText.isEmpty || selectedImageData != nil || confirmedMediaId != nil
        return hasContent
    }
}

public protocol ComposerDraftStoreProtocol: Sendable {
    func save(draft: ComposerDraft)
    func load() -> ComposerDraft?
    func clear()
}

public final class FileComposerDraftStore: ComposerDraftStoreProtocol, @unchecked Sendable {
    public static let shared = FileComposerDraftStore()

    private let fileManager: FileManager
    private let fileName: String
    private let directoryURL: URL?
    private let lock = NSLock()

    public init(
        fileManager: FileManager = .default,
        fileName: String = "us_composer_draft.json",
        directoryURL: URL? = nil
    ) {
        self.fileManager = fileManager
        self.fileName = fileName
        self.directoryURL = directoryURL ?? fileManager.urls(for: .documentDirectory, in: .userDomainMask).first
    }

    private var fileURL: URL? {
        directoryURL?.appendingPathComponent(fileName)
    }

    public func save(draft: ComposerDraft) {
        guard let url = fileURL else { return }
        lock.lock()
        defer { lock.unlock() }
        do {
            let data = try JSONEncoder().encode(draft)
            try data.write(to: url, options: .atomic)
        } catch {
            // Disk write failure silently ignored
        }
    }

    public func load() -> ComposerDraft? {
        guard let url = fileURL, fileManager.fileExists(atPath: url.path) else {
            return nil
        }
        lock.lock()
        defer { lock.unlock() }
        do {
            let data = try Data(contentsOf: url)
            let draft = try JSONDecoder().decode(ComposerDraft.self, from: data)
            if draft.isValidForRestoration {
                return draft
            } else {
                // Drop half-formed draft and remove stale file
                try? fileManager.removeItem(at: url)
                return nil
            }
        } catch {
            try? fileManager.removeItem(at: url)
            return nil
        }
    }

    public func clear() {
        guard let url = fileURL, fileManager.fileExists(atPath: url.path) else { return }
        lock.lock()
        defer { lock.unlock() }
        try? fileManager.removeItem(at: url)
    }
}

public final class InMemoryComposerDraftStore: ComposerDraftStoreProtocol, @unchecked Sendable {
    private var draft: ComposerDraft?
    private let lock = NSLock()

    public init(initialDraft: ComposerDraft? = nil) {
        self.draft = initialDraft
    }

    public func save(draft: ComposerDraft) {
        lock.lock()
        defer { lock.unlock() }
        self.draft = draft
    }

    public func load() -> ComposerDraft? {
        lock.lock()
        defer { lock.unlock() }
        guard let draft = draft, draft.isValidForRestoration else { return nil }
        return draft
    }

    public func clear() {
        lock.lock()
        defer { lock.unlock() }
        self.draft = nil
    }
}
