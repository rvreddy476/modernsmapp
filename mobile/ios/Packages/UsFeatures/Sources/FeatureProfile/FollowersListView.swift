import SwiftUI
import UsModel
import UsDesignSystem
import UsNetwork

public enum FollowListType: String {
    case followers = "Followers"
    case following = "Following"
}

@Observable
public final class FollowListViewModel: @unchecked Sendable {
    public var users: [Author] = []
    public var followingSet: Set<String> = []
    public var searchQuery: String = ""
    public var isLoading: Bool = false
    public var errorMessage: String? = nil

    private let userId: String
    private let type: FollowListType
    private let client: APIClientProtocol

    public init(userId: String, type: FollowListType, client: APIClientProtocol = APIClient()) {
        self.userId = userId
        self.type = type
        self.client = client
    }

    public var filteredUsers: [Author] {
        let clean = searchQuery.trimmingCharacters(in: .whitespacesAndNewlines).lowercased()
        guard !clean.isEmpty else { return users }
        return users.filter {
            ($0.displayName?.lowercased().contains(clean) ?? false) ||
            ($0.username?.lowercased().contains(clean) ?? false)
        }
    }

    @MainActor
    public func loadUsers() async {
        isLoading = true
        errorMessage = nil
        do {
            let endpoint = type == .followers ? "v1/graph/\(userId)/followers" : "v1/graph/\(userId)/following"
            let response: ApiEnvelope<[Author]> = try await client.requestEnvelope(
                endpoint: endpoint,
                method: "GET",
                query: nil,
                body: nil
            )
            self.users = response.data
            if type == .following {
                self.followingSet = Set(response.data.map { $0.id })
            }
        } catch {
            self.errorMessage = error.localizedDescription
        }
        self.isLoading = false
    }

    public func toggleFollow(for targetId: String) {
        let isFollowing = followingSet.contains(targetId)
        if isFollowing {
            followingSet.remove(targetId)
        } else {
            followingSet.insert(targetId)
        }

        Task {
            let endpoint = "v1/graph/follow/\(targetId)"
            let method = isFollowing ? "DELETE" : "POST"
            let _: [String: String] = (try? await client.request(endpoint: endpoint, method: method, query: nil, body: nil)) ?? [:]
        }
    }
}

public struct FollowersListView: View {
    @State private var viewModel: FollowListViewModel
    public let type: FollowListType
    public let onOpenAuthor: (String) -> Void

    public init(
        userId: String,
        type: FollowListType,
        client: APIClientProtocol = APIClient(),
        onOpenAuthor: @escaping (String) -> Void = { _ in }
    ) {
        self.type = type
        self.onOpenAuthor = onOpenAuthor
        _viewModel = State(initialValue: FollowListViewModel(userId: userId, type: type, client: client))
    }

    public var body: some View {
        ZStack {
            UsColors.bgPrimary
                .ignoresSafeArea()

            VStack(spacing: 0) {
                // Search bar
                searchField

                if viewModel.isLoading && viewModel.users.isEmpty {
                    UsLoadingState(message: "Loading \(type.rawValue.lowercased())...")
                } else if let error = viewModel.errorMessage, viewModel.users.isEmpty {
                    UsErrorState(message: error) {
                        Task { await viewModel.loadUsers() }
                    }
                } else if viewModel.filteredUsers.isEmpty {
                    UsEmptyState(
                        title: "No \(type.rawValue)",
                        detail: "No accounts found."
                    )
                } else {
                    List(viewModel.filteredUsers) { author in
                        userRow(author)
                            .listRowBackground(UsColors.bgPrimary)
                            .listRowSeparatorTint(UsColors.borderSubtle)
                    }
                    .listStyle(.plain)
                    .scrollContentBackground(.hidden)
                }
            }
        }
        .navigationTitle(type.rawValue)
        .navigationBarTitleDisplayMode(.inline)
        .task {
            await viewModel.loadUsers()
        }
    }

    private var searchField: some View {
        HStack(spacing: 8) {
            Image(systemName: "magnifyingglass")
                .foregroundColor(UsColors.textMuted)
            TextField("Search", text: $viewModel.searchQuery)
                .textFieldStyle(.plain)
                .font(.system(size: 14))
                .foregroundColor(UsColors.textPrimary)
        }
        .padding(.horizontal, 12)
        .padding(.vertical, 8)
        .background(UsColors.bgSecondary)
        .clipShape(RoundedRectangle(cornerRadius: 10))
        .padding(.horizontal, 16)
        .padding(.vertical, 8)
    }

    @ViewBuilder
    private func userRow(_ author: Author) -> some View {
        HStack(spacing: 12) {
            Button(action: { onOpenAuthor(author.id) }) {
                HStack(spacing: 12) {
                    UsAvatar(name: author.nameForDisplay, url: author.avatarUrl, size: .medium)
                    VStack(alignment: .leading, spacing: 2) {
                        Text(author.nameForDisplay)
                            .font(.system(size: 15, weight: .semibold))
                            .foregroundColor(UsColors.textPrimary)
                        if let u = author.username {
                            Text("@\(u)")
                                .font(.system(size: 12))
                                .foregroundColor(UsColors.textMuted)
                        }
                    }
                }
            }
            .buttonStyle(.plain)

            Spacer()

            Button(action: { viewModel.toggleFollow(for: author.id) }) {
                let isFollowing = viewModel.followingSet.contains(author.id)
                Text(isFollowing ? "Following" : "Follow")
                    .font(.system(size: 13, weight: .bold))
                    .foregroundColor(isFollowing ? UsColors.textPrimary : .black)
                    .padding(.horizontal, 14)
                    .padding(.vertical, 6)
                    .background(isFollowing ? UsColors.bgSecondary : Color.white)
                    .clipShape(Capsule())
                    .overlay(Capsule().stroke(UsColors.borderMedium, lineWidth: isFollowing ? 1 : 0))
            }
            .buttonStyle(.plain)
        }
        .padding(.vertical, 4)
    }
}
