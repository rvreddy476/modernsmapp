import SwiftUI
import UsModel

public struct PostCardView: View {
    public let item: FeedItem
    public let overlay: EngagementOverlay
    public let onClick: () -> Void
    public let onAuthorClick: () -> Void
    public let onReact: () -> Void
    public let onComment: () -> Void
    public let onRepost: () -> Void
    public let onBookmark: () -> Void
    public let onShare: () -> Void
    public let onTip: (() -> Void)?

    public init(
        item: FeedItem,
        overlay: EngagementOverlay = EngagementOverlay(),
        onClick: @escaping () -> Void = {},
        onAuthorClick: @escaping () -> Void = {},
        onReact: @escaping () -> Void = {},
        onComment: @escaping () -> Void = {},
        onRepost: @escaping () -> Void = {},
        onBookmark: @escaping () -> Void = {},
        onShare: @escaping () -> Void = {},
        onTip: (() -> Void)? = nil
    ) {
        self.item = item
        self.overlay = overlay
        self.onClick = onClick
        self.onAuthorClick = onAuthorClick
        self.onReact = onReact
        self.onComment = onComment
        self.onRepost = onRepost
        self.onBookmark = onBookmark
        self.onShare = onShare
        self.onTip = onTip
    }

    public var body: some View {
        VStack(alignment: .leading, spacing: 12) {
            // Header: Avatar + Author Info + Verified Badge + Pinned Badge + More
            HStack(spacing: 12) {
                Button(action: onAuthorClick) {
                    UsAvatar(
                        name: item.author.nameForDisplay,
                        url: item.author.avatarUrl,
                        size: .medium
                    )
                }
                .buttonStyle(.plain)

                VStack(alignment: .leading, spacing: 2) {
                    HStack(spacing: 6) {
                        Text(item.author.nameForDisplay)
                            .font(.system(size: 15, weight: .semibold))
                            .foregroundColor(UsColors.textPrimary)
                            .lineLimit(1)

                        // Verified Identity Seal
                        Image(systemName: "checkmark.seal.fill")
                            .font(.system(size: 13))
                            .foregroundColor(UsColors.postbookPrimary)

                        if item.isPinned {
                            Text("• Pinned")
                                .font(.system(size: 12, weight: .medium))
                                .foregroundColor(UsColors.postbookPrimary)
                        }
                    }

                    Text(item.createdAt)
                        .font(.system(size: 12, weight: .regular))
                        .foregroundColor(UsColors.textMuted)
                }

                Spacer()

                // Tip Creator Chip (Super-App Creator Economy)
                if let onTip = onTip {
                    Button(action: onTip) {
                        HStack(spacing: 4) {
                            Text("☕️")
                            Text("Tip")
                                .font(.system(size: 11, weight: .bold))
                        }
                        .padding(.horizontal, 10)
                        .padding(.vertical, 4)
                        .background(UsColors.bgSecondary)
                        .foregroundColor(UsColors.textPrimary)
                        .clipShape(Capsule())
                        .overlay(Capsule().stroke(UsColors.borderSubtle, lineWidth: 1))
                    }
                    .buttonStyle(.plain)
                }

                Button(action: {
                    HapticManager.shared.trigger(.light)
                    ToastManager.shared.show("Post Options & Safety Report", style: .info)
                }) {
                    UsIcons.more()
                        .frame(width: 18, height: 18)
                        .foregroundColor(UsColors.textMuted)
                }
                .buttonStyle(.plain)
            }

            // Post Text
            if !item.text.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty {
                RichTextView(text: item.text)
                    .lineLimit(8)
                    .contentShape(Rectangle())
                    .onTapGesture(perform: onClick)
            }

            // Poll embed if available
            if let poll = item.poll {
                PollWidgetView(
                    poll: poll,
                    onVote: { _ in
                        HapticManager.shared.trigger(.success)
                        ToastManager.shared.show("Vote recorded!", style: .success)
                    }
                )
            }

            // Media attachment
            if let firstMedia = item.media.first, firstMedia.isReady {
                mediaView(for: firstMedia)
            }

            // Social Action Bar
            PostActionBar(
                likeCount: overlay.likeCountOr(item.counts.likes, serverReacted: item.viewer.hasReacted),
                commentCount: item.counts.comments,
                repostCount: overlay.repostCountOr(item.counts.reposts, serverReposted: item.viewer.hasReposted),
                hasReacted: overlay.reactedOr(item.viewer.hasReacted),
                isBookmarked: overlay.bookmarkedOr(item.viewer.isBookmarked),
                hasReposted: overlay.repostedOr(item.viewer.hasReposted),
                canRepost: item.isRepostable,
                onReact: onReact,
                onComment: onComment,
                onRepost: onRepost,
                onBookmark: onBookmark,
                onShare: onShare
            )

            Divider()
                .background(UsColors.borderSubtle)
        }
        .padding(.horizontal, 16)
        .padding(.vertical, 8)
    }

    @ViewBuilder
    private func mediaView(for media: FeedMedia) -> some View {
        ZStack {
            if let urlString = media.posterUrl, let url = URL(string: urlString) {
                AsyncImage(url: url) { phase in
                    switch phase {
                    case .success(let image):
                        image
                            .resizable()
                            .scaledToFill()
                    default:
                        Rectangle()
                            .fill(UsColors.bgTertiary)
                    }
                }
            } else {
                Rectangle()
                    .fill(UsColors.bgTertiary)
            }

            if media.isVideo {
                ZStack {
                    Circle()
                        .fill(Color.black.opacity(0.55))
                        .frame(width: 48, height: 48)
                        .overlay(Circle().stroke(Color.white.opacity(0.2), lineWidth: 1))
                    UsIcons.play()
                        .frame(width: 22, height: 22)
                        .foregroundColor(.white)
                }
            }

            if item.media.count > 1 {
                VStack {
                    HStack {
                        Spacer()
                        Text("1/\(item.media.count)")
                            .font(.system(size: 11, weight: .medium))
                            .foregroundColor(.white)
                            .padding(.horizontal, 8)
                            .padding(.vertical, 4)
                            .background(Color.black.opacity(0.6))
                            .clipShape(Capsule())
                            .padding(8)
                    }
                    Spacer()
                }
            }
        }
        .aspectRatio(CGFloat(media.aspectRatio), contentMode: .fit)
        .clipShape(RoundedRectangle(cornerRadius: 12))
        .overlay(
            RoundedRectangle(cornerRadius: 12)
                .stroke(UsColors.borderSubtle, lineWidth: 0.5)
        )
        .onTapGesture(perform: onClick)
    }
}
