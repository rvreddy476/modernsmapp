import SwiftUI
import UsModel
import UsDesignSystem
import UsNetwork
import FeatureFeed

@Observable
public final class ReelsViewModel: @unchecked Sendable {
    public var reels: [FeedItem] = []
    public var isLoading: Bool = false
    public var errorMessage: String? = nil
    public var currentIndex: Int = 0

    private let client: APIClientProtocol

    public init(client: APIClientProtocol = APIClient()) {
        self.client = client
    }

    @MainActor
    public func loadReels() async {
        isLoading = true
        errorMessage = nil
        do {
            let response: ApiEnvelope<[FeedItem]> = try await client.requestEnvelope(
                endpoint: "v1/feed/reels",
                method: "GET",
                query: nil,
                body: nil
            )
            self.reels = response.data.filter { $0.media.first?.isVideo ?? true }
        } catch {
            self.errorMessage = error.localizedDescription
        }
        self.isLoading = false
    }
}

public struct ReelsView: View {
    @State private var viewModel: ReelsViewModel
    @State private var commentsPostId: String? = nil

    public let onOpenAuthor: (String) -> Void

    public init(
        client: APIClientProtocol = APIClient(),
        onOpenAuthor: @escaping (String) -> Void = { _ in }
    ) {
        _viewModel = State(initialValue: ReelsViewModel(client: client))
        self.onOpenAuthor = onOpenAuthor
    }

    public var body: some View {
        ZStack {
            UsColors.bgPrimary
                .ignoresSafeArea()

            if viewModel.isLoading && viewModel.reels.isEmpty {
                UsLoadingState(message: "Loading reels...")
            } else if let error = viewModel.errorMessage, viewModel.reels.isEmpty {
                UsErrorState(message: error) {
                    Task { await viewModel.loadReels() }
                }
            } else if viewModel.reels.isEmpty {
                UsEmptyState(title: "No Reels yet", detail: "Check back later for video content!")
            } else {
                TabView(selection: $viewModel.currentIndex) {
                    ForEach(Array(viewModel.reels.enumerated()), id: \.element.id) { index, item in
                        ReelPlayerCardView(
                            item: item,
                            isCurrentPage: index == viewModel.currentIndex,
                            onOpenComments: { commentsPostId = item.id },
                            onOpenAuthor: { onOpenAuthor(item.author.id) },
                            onReact: {},
                            onBookmark: {},
                            onShare: {}
                        )
                        .tag(index)
                        .rotationEffect(.degrees(-90))
                        .frame(
                            width: UIScreen.main.bounds.width,
                            height: UIScreen.main.bounds.height
                        )
                    }
                }
                .rotationEffect(.degrees(90))
                .frame(
                    width: UIScreen.main.bounds.height,
                    height: UIScreen.main.bounds.width
                )
                .tabViewStyle(.page(indexDisplayMode: .never))
                .ignoresSafeArea()
            }
        }
        .sheet(item: Binding(
            get: { commentsPostId.map { IdentifiableString(id: $0) } },
            set: { commentsPostId = $0?.id }
        )) { identifiable in
            CommentsSheetView(postId: identifiable.id) {
                commentsPostId = nil
            }
            .presentationDetents([.medium, .large])
            .presentationDragIndicator(.visible)
        }
        .task {
            if viewModel.reels.isEmpty {
                await viewModel.loadReels()
            }
        }
    }
}

private struct IdentifiableString: Identifiable {
    let id: String
}
