import SwiftUI
import UsModel
import UsDesignSystem
import UsNetwork

@Observable
public final class CommentsViewModel: @unchecked Sendable {
    public var comments: [Comment] = []
    public var isLoading: Bool = false
    public var errorMessage: String? = nil
    public var draftText: String = ""
    public var isSubmitting: Bool = false

    private let postId: String
    private let client: APIClientProtocol

    public init(postId: String, client: APIClientProtocol = APIClient()) {
        self.postId = postId
        self.client = client
    }

    @MainActor
    public func loadComments() async {
        isLoading = true
        errorMessage = nil
        do {
            let response: [Comment] = try await client.request(
                endpoint: "v1/posts/\(postId)/comments",
                method: "GET",
                query: nil,
                body: nil
            )
            self.comments = response
        } catch {
            self.errorMessage = error.localizedDescription
        }
        self.isLoading = false
    }

    @MainActor
    public func postComment() async {
        let trimmed = draftText.trimmingCharacters(in: .whitespacesAndNewlines)
        guard !trimmed.isEmpty else { return }

        isSubmitting = true
        do {
            let payload = try JSONEncoder().encode(["text": trimmed])
            let newComment: Comment = try await client.request(
                endpoint: "v1/posts/\(postId)/comments",
                method: "POST",
                query: nil,
                body: payload
            )
            self.comments.append(newComment)
            self.draftText = ""
        } catch {
            self.errorMessage = error.localizedDescription
        }
        self.isSubmitting = false
    }
}

public struct CommentsSheetView: View {
    @State private var viewModel: CommentsViewModel
    public let onDismiss: () -> Void

    public init(postId: String, client: APIClientProtocol = APIClient(), onDismiss: @escaping () -> Void) {
        _viewModel = State(initialValue: CommentsViewModel(postId: postId, client: client))
        self.onDismiss = onDismiss
    }

    public var body: some View {
        NavigationStack {
            VStack(spacing: 0) {
                if viewModel.isLoading && viewModel.comments.isEmpty {
                    UsLoadingState(message: "Loading comments...")
                } else if let error = viewModel.errorMessage, viewModel.comments.isEmpty {
                    UsErrorState(message: error) {
                        Task { await viewModel.loadComments() }
                    }
                } else if viewModel.comments.isEmpty {
                    UsEmptyState(
                        title: "No comments yet",
                        detail: "Be the first to start the conversation!"
                    )
                } else {
                    List(viewModel.comments) { comment in
                        commentRow(comment)
                            .listRowBackground(UsColors.bgSecondary)
                            .listRowSeparatorTint(UsColors.borderSubtle)
                    }
                    .listStyle(.plain)
                    .scrollContentBackground(.hidden)
                }

                // Comment input bar
                inputBar
            }
            .background(UsColors.bgSecondary)
            .navigationTitle("Comments")
            .navigationBarTitleDisplayMode(.inline)
            .toolbar {
                ToolbarItem(placement: .cancellationAction) {
                    Button("Close", action: onDismiss)
                        .foregroundColor(UsColors.textPrimary)
                }
            }
        }
        .task {
            await viewModel.loadComments()
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

    private var inputBar: some View {
        HStack(spacing: 12) {
            TextField("Add a comment...", text: $viewModel.draftText)
                .textFieldStyle(.plain)
                .font(.system(size: 14))
                .foregroundColor(UsColors.textPrimary)
                .padding(.horizontal, 14)
                .padding(.vertical, 10)
                .background(UsColors.bgTertiary)
                .clipShape(Capsule())

            Button(action: {
                Task { await viewModel.postComment() }
            }) {
                if viewModel.isSubmitting {
                    ProgressView()
                        .tint(.white)
                        .frame(width: 28, height: 28)
                } else {
                    Image(systemName: "arrow.up.circle.fill")
                        .font(.system(size: 28))
                        .foregroundColor(
                            viewModel.draftText.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty
                                ? UsColors.textMuted
                                : UsColors.postbookPrimary
                        )
                }
            }
            .disabled(viewModel.draftText.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty || viewModel.isSubmitting)
        }
        .padding(.horizontal, 16)
        .padding(.vertical, 10)
        .background(UsColors.bgPrimary)
    }
}
