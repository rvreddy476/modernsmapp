import SwiftUI
import UsModel
import UsDesignSystem
import UsNetwork

@Observable
public final class ExploreViewModel: @unchecked Sendable {
    public var query: String = ""
    public var searchResults: [FeedItem] = []
    public var trendingItems: [FeedItem] = []
    public var isSearching: Bool = false
    public var errorMessage: String? = nil

    private let client: APIClientProtocol
    private var searchTask: Task<Void, Never>?

    public init(client: APIClientProtocol = APIClient()) {
        self.client = client
    }

    @MainActor
    public func loadTrending() async {
        do {
            let response: ApiEnvelope<[FeedItem]> = try await client.requestEnvelope(
                endpoint: "v1/feed/explore",
                method: "GET",
                query: nil,
                body: nil
            )
            self.trendingItems = response.data
        } catch {
            // Fallback gracefully
        }
    }

    public func onQueryChanged(_ newQuery: String) {
        searchTask?.cancel()
        guard !newQuery.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty else {
            searchResults = []
            isSearching = false
            return
        }

        isSearching = true
        searchTask = Task {
            try? await Task.sleep(nanoseconds: 300_000_000) // 300ms debounce
            guard !Task.isCancelled else { return }

            do {
                let results: ApiEnvelope<[FeedItem]> = try await client.requestEnvelope(
                    endpoint: "v1/search",
                    method: "GET",
                    query: ["q": newQuery],
                    body: nil
                )
                await MainActor.run {
                    self.searchResults = results.data
                    self.isSearching = false
                }
            } catch {
                await MainActor.run {
                    self.isSearching = false
                }
            }
        }
    }
}

public struct ExploreView: View {
    @State private var viewModel: ExploreViewModel
    public let onOpenPost: (String) -> Void
    public let onOpenAuthor: (String) -> Void

    public init(
        client: APIClientProtocol = APIClient(),
        onOpenPost: @escaping (String) -> Void = { _ in },
        onOpenAuthor: @escaping (String) -> Void = { _ in }
    ) {
        _viewModel = State(initialValue: ExploreViewModel(client: client))
        self.onOpenPost = onOpenPost
        self.onOpenAuthor = onOpenAuthor
    }

    private let columns = [
        GridItem(.flexible(), spacing: 2),
        GridItem(.flexible(), spacing: 2),
        GridItem(.flexible(), spacing: 2)
    ]

    public var body: some View {
        NavigationStack {
            ZStack {
                UsColors.bgPrimary
                    .ignoresSafeArea()

                VStack(spacing: 0) {
                    // Search Bar
                    searchBar

                    // Content
                    if viewModel.isSearching {
                        UsLoadingState(message: "Searching...")
                    } else if !viewModel.query.isEmpty {
                        if viewModel.searchResults.isEmpty {
                            UsEmptyState(title: "No results", detail: "Try searching for another topic or creator.")
                        } else {
                            searchResultsList
                        }
                    } else {
                        trendingGrid
                    }
                }
            }
            .navigationTitle("Explore")
            .navigationBarTitleDisplayMode(.inline)
            .task {
                if viewModel.trendingItems.isEmpty {
                    await viewModel.loadTrending()
                }
            }
        }
    }

    private var searchBar: some View {
        HStack(spacing: 10) {
            Image(systemName: "magnifyingglass")
                .foregroundColor(UsColors.textMuted)

            TextField("Search posts, topics, creators", text: $viewModel.query)
                .textFieldStyle(.plain)
                .font(.system(size: 15))
                .foregroundColor(UsColors.textPrimary)
                .onChange(of: viewModel.query) { _, newValue in
                    viewModel.onQueryChanged(newValue)
                }

            if !viewModel.query.isEmpty {
                Button(action: { viewModel.query = "" }) {
                    Image(systemName: "xmark.circle.fill")
                        .foregroundColor(UsColors.textMuted)
                }
            }
        }
        .padding(.horizontal, 14)
        .padding(.vertical, 10)
        .background(UsColors.bgSecondary)
        .clipShape(RoundedRectangle(cornerRadius: 12))
        .padding(.horizontal, 16)
        .padding(.vertical, 8)
    }

    private var searchResultsList: some View {
        ScrollView {
            LazyVStack(spacing: 12) {
                ForEach(viewModel.searchResults) { item in
                    PostCardView(
                        item: item,
                        onClick: { onOpenPost(item.id) },
                        onAuthorClick: { onOpenAuthor(item.author.id) }
                    )
                }
            }
            .padding(.top, 8)
        }
    }

    private var trendingGrid: some View {
        ScrollView {
            LazyVGrid(columns: columns, spacing: 2) {
                ForEach(viewModel.trendingItems) { post in
                    Button(action: { onOpenPost(post.id) }) {
                        ZStack {
                            if let firstMedia = post.media.first,
                               let posterString = firstMedia.posterUrl,
                               let posterURL = URL(string: posterString) {
                                AsyncImage(url: posterURL) { phase in
                                    switch phase {
                                    case .success(let image):
                                        image.resizable().scaledToFill()
                                    default:
                                        Rectangle().fill(UsColors.bgTertiary)
                                    }
                                }
                            } else {
                                Rectangle().fill(UsColors.bgTertiary)
                                Text(post.text)
                                    .font(.system(size: 11))
                                    .foregroundColor(UsColors.textMuted)
                                    .padding(8)
                            }
                        }
                        .frame(height: 124)
                        .clipped()
                    }
                    .buttonStyle(.plain)
                }
            }
        }
    }
}
