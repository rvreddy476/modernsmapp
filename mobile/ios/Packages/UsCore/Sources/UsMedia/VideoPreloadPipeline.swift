import Foundation
import AVFoundation

public final class VideoPreloadPipeline: @unchecked Sendable {
    public static let shared = VideoPreloadPipeline()

    private var preloadedItems: [URL: AVPlayerItem] = [:]
    private let lock = NSLock()
    private let maxPreloadCount = 4

    public init() {}

    public func preload(urls: [URL]) {
        lock.lock()
        defer { lock.unlock() }

        // Remove old items beyond capacity
        if preloadedItems.count > maxPreloadCount {
            preloadedItems.removeAll()
        }

        for url in urls.prefix(maxPreloadCount) {
            guard preloadedItems[url] == nil else { continue }
            let asset = AVURLAsset(url: url)
            let item = AVPlayerItem(asset: asset)
            preloadedItems[url] = item

            // Warm asset keys in background
            Task.detached {
                _ = try? await asset.load(.isPlayable, .duration)
            }
        }
    }

    public func item(for url: URL) -> AVPlayerItem {
        lock.lock()
        defer { lock.unlock() }

        if let preloaded = preloadedItems.removeValue(forKey: url) {
            return preloaded
        }
        return AVPlayerItem(url: url)
    }

    public func clear() {
        lock.lock()
        defer { lock.unlock() }
        preloadedItems.removeAll()
    }
}
