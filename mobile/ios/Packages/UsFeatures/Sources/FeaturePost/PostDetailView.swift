import SwiftUI
import UsModel
import UsDesignSystem
import UsNetwork
import UsMedia

@Observable
public final class PostDetailViewModel: @unchecked Sendable {
    public var post: FeedItem?
    public var comments: [Comment] = []
    public var isLoading: Bool = false
    public var errorMessage: String? = nil
    public var draftComment: String = ""
    public var isSubmittingComment: Bool = false

    private let postId: String
    private let client: APIClientProtocol

    public init(postId: String, client: APIClientProtocol = APIClient()) {
        self.postId = postId
        self.client = client
    }

    @MainActor
    public func loadData() async {
        isLoading = true
        errorMessage = nil
        do {
            async let postTask: FeedItem = client.request(endpoint: "v1/posts/\(postId)", method: "GET", query: nil, body: nil)
            async let commentsTask: [Comment] = client.request(endpoint: "v1/posts/\(postId)/comments", method: "GET", query: nil, body: nil)

            self.post = try await postTask
            self.comments = try await commentsTask
        } catch {
            self.errorMessage = error.localizedDescription
        }
        self.isLoading = false
    }

    @MainActor
    public func sendComment() async {
        let trimmed = draftComment.trimmingCharacters(in: .whitespacesAndNewlines)
        guard !trimmed.isEmpty else { return }

        isSubmittingComment = true
        do {
            let body = try JSONEncoder().encode(["text": trimmed])
            let newComment: Comment = try await client.request(
                endpoint: "v1/posts/\(postId)/comments",
                method: "POST",
                query: nil,
                body: body
            )
            self.comments.append(newComment)
            self.draftComment = ""
        } catch {
            self.errorMessage = error.localizedDescription
        }
        self.isSubmittingComment = false
    }
}

public struct PostDetailView: View {
    @State private var viewModel: PostDetailViewModel
    public let onOpenAuthor: (String) -> Void

    public init(
        postId: String,
        client: APIClientProtocol = APIClient(),
        onOpenAuthor: @escaping (String) -> Void = { _ in }
    ) {
        _viewModel = State(initialValue: PostDetailViewModel(postId: postId, client: client))
        self.onOpenAuthor = onOpenAuthor
    }

    public var body: some View {
        ZStack {
            UsColors.bgPrimary
                .ignoresSafeArea()

            if viewModel.isLoading && viewModel.post == nil {
                UsLoadingState(message: "Loading post...")
            } else if let error = viewModel.errorMessage, viewModel.post == nil {
                UsErrorState(message: error) {
                    Task { await viewModel.loadData() }
                }
            } else if let post = viewModel.post {
                VStack(spacing: 0) {
                    ScrollView {
                        VStack(alignment: .leading, spacing: 16) {
                            PostCardView(
                                item: post,
                                onClick: {},
                                onAuthorClick: { onOpenAuthor(post.author.id) }
                            )

                            // Comments Section Header
                            Text("Comments (\(viewModel.comments.count))")
                                .font(.system(size: 17, weight: .bold))
                                .foregroundColor(UsColors.textPrimary)
                                .padding(.horizontal, 16)

                            // Comments List
                            LazyVStack(spacing: 12) {
                                ForEach(viewModel.comments) { comment in
                                    commentRow(comment)
                                }
                            }
                            .padding(.horizontal, 16)
                        }
                        .padding(.vertical, 12)
                    }

                    // Comment input at bottom
                    commentInputBar
                }
            }
        }
        .navigationTitle("Post")
        .navigationBarTitleDisplayMode(.inline)
        .task {
            if viewModel.post == nil {
                await viewModel.loadData()
            }
        }
    }

    @ViewBuilder
    private func commentRow(_ comment: Comment) -> some View {
        HStack(alignment: .top, spacing: 12) {
            UsAvatar(name: comment.author.nameForDisplay, url: comment.author.avatarUrl, size: .small)
            VStack(alignment: .leading, spacing: 4) {
                HStack(spacing: 6) {
                    Text(comment.author.nameForDisplay)
                        .font(.system(size: 13, weight: .semibold))
                        .foregroundColor(UsColors.textPrimary)
                    Text(comment.createdAt)
                        .font(.system(size: 11))
                        .foregroundColor(UsColors.textMuted)
                }
                Text(comment.text)
                    .font(.system(size: 14))
                    .foregroundColor(UsColors.textSecondary)
            }
            Spacer()
        }
        .padding(.vertical, 4)
    }

    private var commentInputBar: some View {
        HStack(spacing: 12) {
            TextField("Add a comment...", text: $viewModel.draftComment)
                .textFieldStyle(.plain)
                .font(.system(size: 14))
                .foregroundColor(UsColors.textPrimary)
                .padding(.horizontal, 14)
                .padding(.vertical, 10)
                .background(UsColors.bgTertiary)
                .clipShape(Capsule())

            Button(action: {
                Task { await viewModel.sendComment() }
            }) {
                if viewModel.isSubmittingComment {
                    ProgressView()
                        .tint(.white)
                        .frame(width: 28, height: 28)
                } else {
                    Image(systemName: "arrow.up.circle.fill")
                        .font(.system(size: 28))
                        .foregroundColor(
                            viewModel.draftComment.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty
                                ? UsColors.textMuted
                                : UsColors.postbookPrimary
                        )
                }
            }
            .disabled(viewModel.draftComment.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty || viewModel.isSubmittingComment)
        }
        .padding(.horizontal, 16)
        .padding(.vertical, 10)
        .background(UsColors.bgSecondary)
    }
}
