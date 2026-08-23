import SwiftUI
import UsModel
import UsDesignSystem
import UsNetwork

@Observable
public final class WatchFeedViewModel: @unchecked Sendable {
    public var videos: [FeedItem] = []
    public var isLoading: Bool = false
    public var errorMessage: String? = nil

    private let client: APIClientProtocol

    public init(client: APIClientProtocol = APIClient()) {
        self.client = client
    }

    @MainActor
    public func loadVideos() async {
        isLoading = true
        errorMessage = nil
        do {
            let response: ApiEnvelope<[FeedItem]> = try await client.requestEnvelope(
                endpoint: "v1/feed/watch",
                method: "GET",
                query: nil,
                body: nil
            )
            self.videos = response.data.filter { $0.postType == "long_video" || $0.postType == "video" }
        } catch {
            self.errorMessage = error.localizedDescription
        }
        self.isLoading = false
    }
}

public struct WatchFeedView: View {
    @State private var viewModel: WatchFeedViewModel
    public let onOpenVideo: (String) -> Void
    public let onOpenAuthor: (String) -> Void

    public init(
        client: APIClientProtocol = APIClient(),
        onOpenVideo: @escaping (String) -> Void = { _ in },
        onOpenAuthor: @escaping (String) -> Void = { _ in }
    ) {
        _viewModel = State(initialValue: WatchFeedViewModel(client: client))
        self.onOpenVideo = onOpenVideo
        self.onOpenAuthor = onOpenAuthor
    }

    public var body: some View {
        NavigationStack {
            ZStack {
                UsColors.bgPrimary
                    .ignoresSafeArea()

                if viewModel.isLoading && viewModel.videos.isEmpty {
                    UsLoadingState(message: "Loading videos...")
                } else if let error = viewModel.errorMessage, viewModel.videos.isEmpty {
                    UsErrorState(message: error) {
                        Task { await viewModel.loadVideos() }
                    }
                } else if viewModel.videos.isEmpty {
                    UsEmptyState(title: "No Videos", detail: "Long-form watch videos will appear here.")
                } else {
                    ScrollView {
                        LazyVStack(spacing: 20) {
                            ForEach(viewModel.videos) { item in
                                videoFeedCard(item)
                            }
                        }
                        .padding(.vertical, 12)
                    }
                    .refreshable {
                        await viewModel.loadVideos()
                    }
                }
            }
            .navigationTitle("Watch")
            .task {
                if viewModel.videos.isEmpty {
                    await viewModel.loadVideos()
                }
            }
        }
    }

    @ViewBuilder
    private func videoFeedCard(_ item: FeedItem) -> some View {
        Button(action: { onOpenVideo(item.id) }) {
            VStack(alignment: .leading, spacing: 10) {
                // Large 16:9 Thumbnail
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

                    Text("18:42")
                        .font(.system(size: 11, weight: .bold))
                        .foregroundColor(.white)
                        .padding(.horizontal, 6)
                        .padding(.vertical, 2)
                        .background(Color.black.opacity(0.8))
                        .clipShape(RoundedRectangle(cornerRadius: 4))
                        .padding(10)
                }
                .aspectRatio(16 / 9, contentMode: .fit)
                .clipShape(RoundedRectangle(cornerRadius: 12))

                // Video Info Row
                HStack(alignment: .top, spacing: 12) {
                    Button(action: { onOpenAuthor(item.author.id) }) {
                        UsAvatar(name: item.author.nameForDisplay, url: item.author.avatarUrl, size: .medium)
                    }
                    .buttonStyle(.plain)

                    VStack(alignment: .leading, spacing: 3) {
                        Text(item.text)
                            .font(.system(size: 15, weight: .semibold))
                            .foregroundColor(UsColors.textPrimary)
                            .lineLimit(2)

                        HStack(spacing: 6) {
                            Text(item.author.nameForDisplay)
                            Text("•")
                            Text("\(item.viewCount) views")
                            Text("•")
                            Text(item.createdAt)
                        }
                        .font(.system(size: 12))
                        .foregroundColor(UsColors.textMuted)
                    }

                    Spacer()
                }
                .padding(.horizontal, 4)
            }
            .padding(.horizontal, 16)
        }
        .buttonStyle(.plain)
    }
}
