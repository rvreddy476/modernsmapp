import SwiftUI
import UsModel
import UsDesignSystem

public struct FanClubPost: Identifiable {
    public let id: String
    public let title: String
    public let timestamp: String
    public let isAudioNote: Bool
    public let likesCount: Int

    public init(id: String, title: String, timestamp: String, isAudioNote: Bool = false, likesCount: Int = 42) {
        self.id = id
        self.title = title
        self.timestamp = timestamp
        self.isAudioNote = isAudioNote
        self.likesCount = likesCount
    }
}

public struct FanClubLoungeView: View {
    public let creatorName: String
    public let onDismiss: () -> Void

    @State private var posts: [FanClubPost] = [
        FanClubPost(id: "fcp-1", title: "🔒 VIP Voice Note: Behind the scenes of tomorrow's video launch!", timestamp: "2 hours ago", isAudioNote: true, likesCount: 128),
        FanClubPost(id: "fcp-2", title: "🔒 Early Access: Raw camera LUT presets download link", timestamp: "Yesterday", isAudioNote: false, likesCount: 94),
        FanClubPost(id: "fcp-3", title: "🔒 Member Poll: Which city should we do our next creator meetup in?", timestamp: "3 days ago", isAudioNote: false, likesCount: 312)
    ]

    public init(
        creatorName: String = "Sarah Chen",
        onDismiss: @escaping () -> Void = {}
    ) {
        self.creatorName = creatorName
        self.onDismiss = onDismiss
    }

    public var body: some View {
        NavigationStack {
            ZStack {
                UsColors.bgPrimary
                    .ignoresSafeArea()

                ScrollView {
                    VStack(alignment: .leading, spacing: 18) {
                        // VIP Club Header
                        HStack(spacing: 12) {
                            ZStack {
                                Circle()
                                    .fill(
                                        LinearGradient(colors: [Color.yellow, Color.orange], startPoint: .topLeading, endPoint: .bottomTrailing)
                                    )
                                    .frame(width: 48, height: 48)

                                Image(systemName: "crown.fill")
                                    .foregroundColor(.black)
                                    .font(.system(size: 22))
                            }

                            VStack(alignment: .leading, spacing: 2) {
                                Text("\(creatorName)'s VIP Fan Lounge")
                                    .font(.system(size: 16, weight: .bold))
                                    .foregroundColor(UsColors.textPrimary)

                                Text("Founder Tier Member • 420 VIPs inside")
                                    .font(.system(size: 11))
                                    .foregroundColor(UsColors.textMuted)
                            }
                        }
                        .padding(14)
                        .frame(maxWidth: .infinity, alignment: .leading)
                        .background(UsColors.bgSecondary)
                        .clipShape(RoundedRectangle(cornerRadius: 16))

                        Text("Exclusive Subscriber Drops")
                            .font(.system(size: 15, weight: .bold))
                            .foregroundColor(UsColors.textPrimary)

                        LazyVStack(spacing: 12) {
                            ForEach(posts) { post in
                                fanPostCard(post)
                            }
                        }
                    }
                    .padding(16)
                }
            }
            .navigationTitle("Fan Lounge")
            .navigationBarTitleDisplayMode(.inline)
            .toolbar {
                ToolbarItem(placement: .cancellationAction) {
                    Button("Close", action: onDismiss)
                        .foregroundColor(UsColors.textMuted)
                }
            }
        }
    }

    @ViewBuilder
    private func fanPostCard(_ post: FanClubPost) -> some View {
        VStack(alignment: .leading, spacing: 10) {
            HStack {
                Text(post.timestamp)
                    .font(.system(size: 11))
                    .foregroundColor(UsColors.textMuted)

                Spacer()

                HStack(spacing: 4) {
                    Image(systemName: "heart.fill")
                        .foregroundColor(UsColors.liveRed)
                        .font(.system(size: 11))
                    Text("\(post.likesCount)")
                        .font(.system(size: 11, weight: .semibold))
                        .foregroundColor(UsColors.textSecondary)
                }
            }

            Text(post.title)
                .font(.system(size: 14, weight: .semibold))
                .foregroundColor(UsColors.textPrimary)

            if post.isAudioNote {
                HStack(spacing: 8) {
                    Image(systemName: "play.circle.fill")
                        .font(.system(size: 24))
                        .foregroundColor(UsColors.postbookPrimary)

                    Text("Exclusive Voice Note (1:45)")
                        .font(.system(size: 12, weight: .bold))
                        .foregroundColor(UsColors.textPrimary)
                }
                .padding(10)
                .frame(maxWidth: .infinity, alignment: .leading)
                .background(UsColors.bgTertiary)
                .clipShape(RoundedRectangle(cornerRadius: 10))
            }
        }
        .padding(14)
        .background(UsColors.bgSecondary)
        .clipShape(RoundedRectangle(cornerRadius: 14))
    }
}
