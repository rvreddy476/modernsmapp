import SwiftUI
import UsModel

public struct PostActionBar: View {
    public let likeCount: Int
    public let commentCount: Int
    public let repostCount: Int
    public let hasReacted: Bool
    public let isBookmarked: Bool
    public let hasReposted: Bool
    public let canRepost: Bool
    public let onReact: () -> Void
    public let onComment: () -> Void
    public let onRepost: () -> Void
    public let onBookmark: () -> Void
    public let onShare: () -> Void

    public init(
        likeCount: Int,
        commentCount: Int,
        repostCount: Int,
        hasReacted: Bool,
        isBookmarked: Bool,
        hasReposted: Bool = false,
        canRepost: Bool = true,
        onReact: @escaping () -> Void,
        onComment: @escaping () -> Void,
        onRepost: @escaping () -> Void,
        onBookmark: @escaping () -> Void,
        onShare: @escaping () -> Void
    ) {
        self.likeCount = likeCount
        self.commentCount = commentCount
        self.repostCount = repostCount
        self.hasReacted = hasReacted
        self.isBookmarked = isBookmarked
        self.hasReposted = hasReposted
        self.canRepost = canRepost
        self.onReact = onReact
        self.onComment = onComment
        self.onRepost = onRepost
        self.onBookmark = onBookmark
        self.onShare = onShare
    }

    public var body: some View {
        HStack(spacing: 0) {
            // Like
            actionButton(
                icon: UsIcons.heart(filled: hasReacted),
                count: likeCount,
                tint: hasReacted ? UsColors.postgramPrimary : UsColors.textMuted,
                action: onReact
            )

            Spacer()

            // Comment
            actionButton(
                icon: UsIcons.comment(),
                count: commentCount,
                tint: UsColors.textMuted,
                action: onComment
            )

            Spacer()

            // Repost
            actionButton(
                icon: UsIcons.repost(active: hasReposted),
                count: repostCount,
                tint: hasReposted ? UsColors.onlineGreen : UsColors.textMuted,
                disabled: !canRepost,
                action: onRepost
            )

            Spacer()

            // Share
            actionButton(
                icon: UsIcons.share(),
                count: nil,
                tint: UsColors.textMuted,
                action: onShare
            )

            Spacer()

            // Bookmark
            actionButton(
                icon: UsIcons.bookmark(filled: isBookmarked),
                count: nil,
                tint: isBookmarked ? UsColors.postbookPrimary : UsColors.textMuted,
                action: onBookmark
            )
        }
        .padding(.vertical, 4)
    }

    @ViewBuilder
    private func actionButton<V: View>(
        icon: V,
        count: Int?,
        tint: Color,
        disabled: Bool = false,
        action: @escaping () -> Void
    ) -> some View {
        Button(action: action) {
            HStack(spacing: 6) {
                icon
                    .frame(width: 20, height: 20)
                    .foregroundColor(tint)
                if let count = count, count > 0 {
                    Text(formatCount(count))
                        .font(.system(size: 13, weight: .medium))
                        .foregroundColor(tint)
                }
            }
            .contentShape(Rectangle())
            .padding(.vertical, 6)
            .padding(.horizontal, 4)
        }
        .buttonStyle(.plain)
        .disabled(disabled)
        .opacity(disabled ? 0.4 : 1.0)
    }

    private func formatCount(_ count: Int) -> String {
        if count >= 1_000_000 {
            return String(format: "%.1fM", Double(count) / 1_000_000)
        }
        if count >= 1_000 {
            return String(format: "%.1fK", Double(count) / 1_000)
        }
        return "\(count)"
    }
}
