import Foundation

public struct EngagementOverlay: Hashable, Sendable {
    public var hasReacted: Bool?
    public var isBookmarked: Bool?
    public var hasReposted: Bool?

    public init(
        hasReacted: Bool? = nil,
        isBookmarked: Bool? = nil,
        hasReposted: Bool? = nil
    ) {
        self.hasReacted = hasReacted
        self.isBookmarked = isBookmarked
        self.hasReposted = hasReposted
    }

    public func reactedOr(_ serverValue: Bool) -> Bool {
        hasReacted ?? serverValue
    }

    public func bookmarkedOr(_ serverValue: Bool) -> Bool {
        isBookmarked ?? serverValue
    }

    public func repostedOr(_ serverValue: Bool) -> Bool {
        hasReposted ?? serverValue
    }

    public func likeCountOr(_ serverLikes: Int, serverReacted: Bool) -> Int {
        guard let local = hasReacted else { return max(0, serverLikes) }
        if local == serverReacted { return max(0, serverLikes) }
        let delta = local ? 1 : -1
        return max(0, serverLikes + delta)
    }

    public func repostCountOr(_ serverReposts: Int, serverReposted: Bool) -> Int {
        guard let local = hasReposted else { return max(0, serverReposts) }
        if local == serverReposted { return max(0, serverReposts) }
        let delta = local ? 1 : -1
        return max(0, serverReposts + delta)
    }
}
