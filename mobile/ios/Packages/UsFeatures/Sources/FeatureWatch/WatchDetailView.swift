import SwiftUI
import UsModel
import UsDesignSystem
import UsNetwork
import UsMedia

@Observable
public final class WatchDetailViewModel: @unchecked Sendable {
    public var post: FeedItem?
    public var relatedVideos: [FeedItem] = []
    public var comments: [Comment] = []
    public var isSubscribed: Bool = false
    public var isDescriptionExpanded: Bool = false
    public var isLoading: Bool = false
    public var errorMessage: String? = nil

    private let postId: String
    private let client: APIClientProtocol

    public init(postId: String, client: APIClientProtocol = APIClient()) {
        self.postId = postId
        self.client = client
    }

    @MainActor
    public func loadVideo() async {
        isLoading = true
        errorMessage = nil
        do {
            async let postTask: FeedItem = client.request(endpoint: "v1/posts/\(postId)", method: "GET", query: nil, body: nil)
            async let relatedTask: ApiEnvelope<[FeedItem]> = client.requestEnvelope(endpoint: "v1/feed/watch", method: "GET", query: nil, body: nil)
            async let commentsTask: [Comment] = client.request(endpoint: "v1/posts/\(postId)/comments", method: "GET", query: nil, body: nil)

            let (p, r, c) = try await (postTask, relatedTask, commentsTask)
            self.post = p
            self.relatedVideos = r.data.filter { $0.id != postId }
            self.comments = c
        } catch {
            self.errorMessage = error.localizedDescription
        }
        self.isLoading = false
    }

    public func toggleSubscribe() {
        isSubscribed.toggle()
        guard let authorId = post?.author.id else { return }
        Task {
            let endpoint = "v1/graph/follow/\(authorId)"
            let method = isSubscribed ? "POST" : "DELETE"
            let _: [String: String] = (try? await client.request(endpoint: endpoint, method: method, query: nil, body: nil)) ?? [:]
        }
    }
}

public struct WatchDetailView: View {
    @State private var viewModel: WatchDetailViewModel
    public let onOpenVideo: (String) -> Void
    public let onOpenAuthor: (String) -> Void

    public init(
        postId: String,
        client: APIClientProtocol = APIClient(),
        onOpenVideo: @escaping (String) -> Void = { _ in },
        onOpenAuthor: @escaping (String) -> Void = { _ in }
    ) {
        _viewModel = State(initialValue: WatchDetailViewModel(postId: postId, client: client))
        self.onOpenVideo = onOpenVideo
        self.onOpenAuthor = onOpenAuthor
    }

    private var videoURL: URL? {
        guard let media = viewModel.post?.media.first,
              let urlString = media.videoStreamUrl ?? media.posterUrl else {
            return nil
        }
        return URL(string: urlString)
    }

    public var body: some View {
        ZStack {
            UsColors.bgPrimary
                .ignoresSafeArea()

            if viewModel.isLoading && viewModel.post == nil {
                UsLoadingState(message: "Loading video...")
            } else if let error = viewModel.errorMessage, viewModel.post == nil {
                UsErrorState(message: error) {
                    Task { await viewModel.loadVideo() }
                }
            } else if let post = viewModel.post {
                VStack(spacing: 0) {
                    // 1. Widescreen 16:9 Video Player
                    if let url = videoURL {
                        WatchVideoPlayerView(videoURL: url)
                    } else {
                        Rectangle()
                            .fill(Color.black)
                            .aspectRatio(16 / 9, contentMode: .fit)
                    }

                    // 2. Details & Related Scroll Content
                    ScrollView {
                        VStack(alignment: .leading, spacing: 16) {
                            // Title & Views
                            VStack(alignment: .leading, spacing: 6) {
                                Text(post.text)
                                    .font(.system(size: 17, weight: .bold))
                                    .foregroundColor(UsColors.textPrimary)

                                HStack(spacing: 8) {
                                    Text("\(post.viewCount) views")
                                    Text("•")
                                    Text(post.createdAt)
                                }
                                .font(.system(size: 13))
                                .foregroundColor(UsColors.textMuted)
                            }
                            .padding(.horizontal, 16)

                            // Channel row + Subscribe
                            HStack(spacing: 12) {
                                Button(action: { onOpenAuthor(post.author.id) }) {
                                    HStack(spacing: 10) {
                                        UsAvatar(name: post.author.nameForDisplay, url: post.author.avatarUrl, size: .medium)
                                        VStack(alignment: .leading, spacing: 2) {
                                            Text(post.author.nameForDisplay)
                                                .font(.system(size: 15, weight: .bold))
                                                .foregroundColor(UsColors.textPrimary)
                                            Text("Channel")
                                                .font(.system(size: 12))
                                                .foregroundColor(UsColors.textMuted)
                                        }
                                    }
                                }
                                .buttonStyle(.plain)

                                Spacer()

                                Button(action: { viewModel.toggleSubscribe() }) {
                                    Text(viewModel.isSubscribed ? "Subscribed" : "Subscribe")
                                        .font(.system(size: 13, weight: .bold))
                                        .foregroundColor(viewModel.isSubscribed ? UsColors.textPrimary : .black)
                                        .padding(.horizontal, 16)
                                        .padding(.vertical, 8)
                                        .background(viewModel.isSubscribed ? UsColors.bgSecondary : Color.white)
                                        .clipShape(Capsule())
                                        .overlay(Capsule().stroke(UsColors.borderMedium, lineWidth: viewModel.isSubscribed ? 1 : 0))
                                }
                            }
                            .padding(.horizontal, 16)

                            Divider().background(UsColors.borderSubtle)

                            // Related Videos
                            Text("Up Next")
                                .font(.system(size: 16, weight: .bold))
                                .foregroundColor(UsColors.textPrimary)
                                .padding(.horizontal, 16)

                            LazyVStack(spacing: 16) {
                                ForEach(viewModel.relatedVideos) { item in
                                    relatedVideoRow(item)
                                }
                            }
                            .padding(.horizontal, 16)
                        }
                        .padding(.top, 12)
                    }
                }
            }
        }
        .navigationBarTitleDisplayMode(.inline)
        .task {
            if viewModel.post == nil {
                await viewModel.loadVideo()
            }
        }
    }

    @ViewBuilder
    private func relatedVideoRow(_ item: FeedItem) -> some View {
        Button(action: { onOpenVideo(item.id) }) {
            HStack(alignment: .top, spacing: 12) {
                // Thumbnail with duration pill
                ZStack(alignment: .bottomTrailing) {
                    if let posterStr = item.media.first?.posterUrl, let posterURL = URL(string: posterStr) {
                        AsyncImage(url: posterURL) { phase in
                            switch phase {
                            case .success(let img):
                                img.resizable().scaledToFill()
                            default:
                                Rectangle().fill(UsColors.bgTertiary)
                            }
                        }
                    } else {
                        Rectangle().fill(UsColors.bgTertiary)
                    }

                    Text("12:40")
                        .font(.system(size: 10, weight: .bold))
                        .foregroundColor(.white)
                        .padding(.horizontal, 6)
                        .padding(.vertical, 2)
                        .background(Color.black.opacity(0.8))
                        .clipShape(RoundedRectangle(cornerRadius: 4))
                        .padding(6)
                }
                .frame(width: 130, height: 74)
                .clipShape(RoundedRectangle(cornerRadius: 8))

                // Metadata
                VStack(alignment: .leading, spacing: 4) {
                    Text(item.text)
                        .font(.system(size: 13, weight: .semibold))
                        .foregroundColor(UsColors.textPrimary)
                        .lineLimit(2)

                    Text(item.author.nameForDisplay)
                        .font(.system(size: 11))
                        .foregroundColor(UsColors.textMuted)

                    Text("\(item.viewCount) views • \(item.createdAt)")
                        .font(.system(size: 11))
                        .foregroundColor(UsColors.textDim)
                }

                Spacer()
            }
        }
        .buttonStyle(.plain)
    }
}
