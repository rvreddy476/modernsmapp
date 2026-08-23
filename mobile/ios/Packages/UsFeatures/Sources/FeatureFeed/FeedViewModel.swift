import SwiftUI
import UsModel
import UsNetwork

@Observable
public final class FeedViewModel: @unchecked Sendable {
    public var items: [FeedItem] = []
    public var overlays: [String: EngagementOverlay] = [:]
    public var isLoading: Bool = false
    public var isAppending: Bool = false
    public var errorMessage: String? = nil
    public var nextCursor: String? = nil

    private let client: APIClientProtocol

    public init(client: APIClientProtocol = APIClient()) {
        self.client = client
        self.items = FeedCacheStore.shared.load()
    }

    @MainActor
    public func loadFeed(refresh: Bool = false) async {
        if refresh {
            if items.isEmpty {
                isLoading = true
            }
            errorMessage = nil
            nextCursor = nil
        } else {
            guard !isAppending && nextCursor != nil else { return }
            isAppending = true
        }

        do {
            var query: [String: String] = [:]
            if let cursor = nextCursor, !refresh {
                query["cursor"] = cursor
            }

            let envelope: ApiEnvelope<[FeedItem]> = try await client.requestEnvelope(
                endpoint: "v1/feed/home",
                method: "GET",
                query: query.isEmpty ? nil : query,
                body: nil
            )

            let fetchedItems = envelope.data ?? []
            if refresh {
                self.items = fetchedItems
                FeedCacheStore.shared.save(items: fetchedItems)
            } else {
                self.items.append(contentsOf: fetchedItems)
            }
            self.nextCursor = envelope.meta?.nextCursor
        } catch {
            if items.isEmpty {
                self.errorMessage = error.localizedDescription
            }
        }

        self.isLoading = false
        self.isAppending = false
    }

    // Engagement actions with optimistic local updates
    public func onReact(postId: String, serverReacted: Bool) {
        var overlay = overlays[postId] ?? EngagementOverlay()
        let current = overlay.reactedOr(serverReacted)
        overlay.hasReacted = !current
        overlays[postId] = overlay

        Task {
            let endpoint = "v1/posts/\(postId)/reactions"
            let method = current ? "DELETE" : "POST"
            let body = current ? nil : try? JSONEncoder().encode(["reaction": "like"])
            do {
                let _: [String: String] = try await client.request(endpoint: endpoint, method: method, query: nil, body: body)
            } catch {
                // Rollback on network failure
                await MainActor.run {
                    var reverted = overlays[postId] ?? EngagementOverlay()
                    reverted.hasReacted = current
                    overlays[postId] = reverted
                }
            }
        }
    }

    public func onBookmark(postId: String, serverBookmarked: Bool) {
        var overlay = overlays[postId] ?? EngagementOverlay()
        let current = overlay.bookmarkedOr(serverBookmarked)
        overlay.isBookmarked = !current
        overlays[postId] = overlay

        Task {
            let endpoint = "v1/posts/\(postId)/bookmark"
            let method = current ? "DELETE" : "POST"
            do {
                let _: [String: String] = try await client.request(endpoint: endpoint, method: method, query: nil, body: nil)
            } catch {
                await MainActor.run {
                    var reverted = overlays[postId] ?? EngagementOverlay()
                    reverted.isBookmarked = current
                    overlays[postId] = reverted
                }
            }
        }
    }

    public func onRepost(postId: String, serverReposted: Bool) {
        var overlay = overlays[postId] ?? EngagementOverlay()
        let current = overlay.repostedOr(serverReposted)
        overlay.hasReposted = !current
        overlays[postId] = overlay

        Task {
            let endpoint = "v1/posts/\(postId)/repost"
            let method = current ? "DELETE" : "POST"
            do {
                let _: [String: String] = try await client.request(endpoint: endpoint, method: method, query: nil, body: nil)
            } catch {
                await MainActor.run {
                    var reverted = overlays[postId] ?? EngagementOverlay()
                    reverted.hasReposted = current
                    overlays[postId] = reverted
                }
            }
        }
    }
}
