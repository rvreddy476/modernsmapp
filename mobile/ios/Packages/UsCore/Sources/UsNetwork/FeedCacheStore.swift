import Foundation
import UsModel

public final class FeedCacheStore: @unchecked Sendable {
    public static let shared = FeedCacheStore()

    private let fileManager = FileManager.default
    private let cacheFileName = "us_feed_cache.json"

    private var cacheURL: URL? {
        guard let dir = fileManager.urls(for: .cachesDirectory, in: .userDomainMask).first else {
            return nil
        }
        return dir.appendingPathComponent(cacheFileName)
    }

    public init() {}

    public func save(items: [FeedItem]) {
        guard let url = cacheURL else { return }
        Task.detached(priority: .background) {
            do {
                let data = try JSONEncoder().encode(items)
                try data.write(to: url, options: .atomic)
            } catch {
                // Ignore cache write errors
            }
        }
    }

    public func load() -> [FeedItem] {
        guard let url = cacheURL, fileManager.fileExists(atPath: url.path) else {
            return []
        }
        do {
            let data = try Data(contentsOf: url)
            return try JSONDecoder().decode([FeedItem].self, from: data)
        } catch {
            return []
        }
    }

    public func clear() {
        guard let url = cacheURL, fileManager.fileExists(atPath: url.path) else { return }
        try? fileManager.removeItem(at: url)
    }
}
