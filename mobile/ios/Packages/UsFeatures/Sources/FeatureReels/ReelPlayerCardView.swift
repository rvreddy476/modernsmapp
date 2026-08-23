import SwiftUI
import UsModel
import UsDesignSystem
import UsNetwork
import UsMedia

public struct ReelPlayerCardView: View {
    public let item: FeedItem
    public let isCurrentPage: Bool
    public let onOpenComments: () -> Void
    public let onOpenAuthor: () -> Void
    public let onReact: () -> Void
    public let onBookmark: () -> Void
    public let onShare: () -> Void

    @State private var isMuted: Bool = false
    @State private var isPlaying: Bool = true
    @State private var showHeartAnimation: Bool = false

    public init(
        item: FeedItem,
        isCurrentPage: Bool,
        onOpenComments: @escaping () -> Void = {},
        onOpenAuthor: @escaping () -> Void = {},
        onReact: @escaping () -> Void = {},
        onBookmark: @escaping () -> Void = {},
        onShare: @escaping () -> Void = {}
    ) {
        self.item = item
        self.isCurrentPage = isCurrentPage
        self.onOpenComments = onOpenComments
        self.onOpenAuthor = onOpenAuthor
        self.onReact = onReact
        self.onBookmark = onBookmark
        self.onShare = onShare
    }

    private var videoURL: URL? {
        guard let firstMedia = item.media.first,
              let urlString = firstMedia.videoStreamUrl ?? firstMedia.posterUrl else {
            return nil
        }
        return URL(string: urlString)
    }

    private var posterURL: URL? {
        guard let firstMedia = item.media.first,
              let urlString = firstMedia.posterUrl else {
            return nil
        }
        return URL(string: urlString)
    }

    public var body: some View {
        ZStack {
            // 1. Full Screen Video Player
            if let url = videoURL {
                VideoPlayerView(
                    videoURL: url,
                    posterURL: posterURL,
                    isPlaying: isCurrentPage && isPlaying,
                    isMuted: isMuted
                )
                .ignoresSafeArea()
            } else {
                Rectangle()
                    .fill(
                        LinearGradient(
                            colors: [Color(red: 0x14/255.0, green: 0x14/255.0, blue: 0x22/255.0),
                                     Color(red: 0x08/255.0, green: 0x08/255.0, blue: 0x10/255.0)],
                            startPoint: .top,
                            endPoint: .bottom
                        )
                    )
                    .ignoresSafeArea()
            }

            // Double tap to like gesture area
            Color.clear
                .contentShape(Rectangle())
                .onTapGesture(count: 2) {
                    onReact()
                    withAnimation(.spring(response: 0.3, dampingFraction: 0.6)) {
                        showHeartAnimation = true
                    }
                    DispatchQueue.main.asyncAfter(deadline: .now() + 0.8) {
                        showHeartAnimation = false
                    }
                }
                .onTapGesture(count: 1) {
                    isPlaying.toggle()
                }

            // Big Double-Tap Pop Heart Animation
            if showHeartAnimation {
                Image(systemName: "heart.fill")
                    .font(.system(size: 90))
                    .foregroundColor(UsColors.postgramPrimary)
                    .shadow(color: .black.opacity(0.4), radius: 10)
                    .transition(.scale.combined(with: .opacity))
            }

            // 2. Bottom Scrim Gradient + Creator Metadata
            VStack {
                Spacer()
                HStack(alignment: .bottom, spacing: 16) {
                    // Left: Creator Avatar, Name, Description, Audio
                    VStack(alignment: .leading, spacing: 10) {
                        Button(action: onOpenAuthor) {
                            HStack(spacing: 8) {
                                UsAvatar(
                                    name: item.author.nameForDisplay,
                                    url: item.author.avatarUrl,
                                    size: .medium
                                )
                                Text(item.author.nameForDisplay)
                                    .font(.system(size: 15, weight: .bold))
                                    .foregroundColor(.white)
                                    .shadow(color: .black.opacity(0.6), radius: 2)
                            }
                        }
                        .buttonStyle(.plain)

                        if !item.text.isEmpty {
                            Text(item.text)
                                .font(.system(size: 14))
                                .foregroundColor(.white)
                                .lineLimit(3)
                                .shadow(color: .black.opacity(0.6), radius: 2)
                        }

                        // Audio track indicator
                        HStack(spacing: 6) {
                            Image(systemName: "music.note")
                                .font(.system(size: 12))
                            Text("Original Audio • \(item.author.nameForDisplay)")
                                .font(.system(size: 12, weight: .medium))
                                .lineLimit(1)
                        }
                        .foregroundColor(.white.opacity(0.85))
                    }
                    .padding(.bottom, 24)

                    Spacer()

                    // Right: Floating Vertical Action Bar
                    VStack(spacing: 20) {
                        verticalActionButton(
                            icon: item.viewer.hasReacted ? "heart.fill" : "heart",
                            count: item.counts.likes,
                            tint: item.viewer.hasReacted ? UsColors.postgramPrimary : .white,
                            action: onReact
                        )

                        verticalActionButton(
                            icon: "bubble.right.fill",
                            count: item.counts.comments,
                            tint: .white,
                            action: onOpenComments
                        )

                        verticalActionButton(
                            icon: "bookmark" + (item.viewer.isBookmarked ? ".fill" : ""),
                            count: nil,
                            tint: item.viewer.isBookmarked ? UsColors.postbookPrimary : .white,
                            action: onBookmark
                        )

                        verticalActionButton(
                            icon: "paperplane.fill",
                            count: nil,
                            tint: .white,
                            action: onShare
                        )

                        Button(action: { isMuted.toggle() }) {
                            Image(systemName: isMuted ? "speaker.slash.fill" : "speaker.wave.2.fill")
                                .font(.system(size: 20))
                                .foregroundColor(.white)
                                .frame(width: 44, height: 44)
                                .background(Color.black.opacity(0.35))
                                .clipShape(Circle())
                        }
                    }
                    .padding(.bottom, 24)
                }
                .padding(.horizontal, 16)
            }
            .background(
                LinearGradient(
                    colors: [Color.clear, Color.black.opacity(0.3), Color.black.opacity(0.75)],
                    startPoint: .center,
                    endPoint: .bottom
                )
                .ignoresSafeArea()
            )
        }
    }

    private func verticalActionButton(
        icon: String,
        count: Int?,
        tint: Color,
        action: @escaping () -> Void
    ) -> some View {
        Button(action: action) {
            VStack(spacing: 4) {
                Image(systemName: icon)
                    .font(.system(size: 26))
                    .foregroundColor(tint)
                    .shadow(color: .black.opacity(0.4), radius: 3)

                if let count = count, count > 0 {
                    Text("\(count)")
                        .font(.system(size: 12, weight: .semibold))
                        .foregroundColor(.white)
                        .shadow(color: .black.opacity(0.4), radius: 2)
                }
            }
        }
        .buttonStyle(.plain)
    }
}
