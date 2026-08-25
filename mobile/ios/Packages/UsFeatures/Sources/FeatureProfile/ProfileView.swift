import SwiftUI
import UsModel
import UsDesignSystem
import UsNetwork
import UsMedia
import FeatureSettings
import FeatureQRCode

public struct UserProfile: Codable, Sendable {
    public let id: String
    public let username: String?
    public let displayName: String?
    public let bio: String?
    public let avatarUrl: String?
    public let followersCount: Int
    public let followingCount: Int
    public let postsCount: Int
    public let isFollowing: Bool

    enum CodingKeys: String, CodingKey {
        case id
        case username
        case displayName = "display_name"
        case bio
        case avatarUrl = "avatar_url"
        case followersCount = "followers_count"
        case followingCount = "following_count"
        case postsCount = "posts_count"
        case isFollowing = "is_following"
    }
}

@Observable
public final class ProfileViewModel: @unchecked Sendable {
    public var profile: UserProfile?
    public var posts: [FeedItem] = []
    public var isLoading: Bool = false
    public var errorMessage: String? = nil
    public var isFollowingLocal: Bool = false

    private let userId: String
    private let client: APIClientProtocol

    public init(userId: String, client: APIClientProtocol = APIClient()) {
        self.userId = userId
        self.client = client
    }

    @MainActor
    public func loadProfile() async {
        isLoading = true
        errorMessage = nil
        do {
            async let profileTask: UserProfile = client.request(endpoint: "v1/profiles/\(userId)", method: "GET", query: nil, body: nil)
            async let postsTask: ApiEnvelope<[FeedItem]> = client.requestEnvelope(endpoint: "v1/posts/user/\(userId)", method: "GET", query: nil, body: nil)

            let (fetchedProfile, fetchedPosts) = try await (profileTask, postsTask)
            self.profile = fetchedProfile
            self.posts = fetchedPosts.data
            self.isFollowingLocal = fetchedProfile.isFollowing
        } catch {
            self.errorMessage = error.localizedDescription
        }
        self.isLoading = false
    }

    public func toggleFollow() {
        let current = isFollowingLocal
        isFollowingLocal = !current

        Task {
            let endpoint = "v1/graph/follow/\(userId)"
            let method = current ? "DELETE" : "POST"
            do {
                let _: [String: String] = try await client.request(endpoint: endpoint, method: method, query: nil, body: nil)
            } catch {
                await MainActor.run {
                    self.isFollowingLocal = current
                }
            }
        }
    }
}

public struct ProfileView: View {
    public let userId: String
    @State private var viewModel: ProfileViewModel
    @State private var showEditProfile: Bool = false
    @State private var showQRModal: Bool = false
    public let onOpenPost: (String) -> Void

    public init(
        userId: String,
        client: APIClientProtocol = APIClient(),
        onOpenPost: @escaping (String) -> Void = { _ in }
    ) {
        self.userId = userId
        _viewModel = State(initialValue: ProfileViewModel(userId: userId, client: client))
        self.onOpenPost = onOpenPost
    }

    private let columns = [
        GridItem(.flexible(), spacing: 2),
        GridItem(.flexible(), spacing: 2),
        GridItem(.flexible(), spacing: 2)
    ]

    private var isMe: Bool {
        userId == "me" || userId == SessionManager.shared.currentSession?.userId
    }

    public var body: some View {
        ZStack {
            UsColors.bgPrimary
                .ignoresSafeArea()

            if viewModel.isLoading && viewModel.profile == nil {
                UsLoadingState(message: "Loading profile...")
            } else if let error = viewModel.errorMessage, viewModel.profile == nil {
                UsErrorState(message: error) {
                    Task { await viewModel.loadProfile() }
                }
            } else if let profile = viewModel.profile {
                ScrollView {
                    VStack(alignment: .leading, spacing: 18) {
                        // Profile Header
                        profileHeader(profile)

                        // Bio
                        if let bio = profile.bio, !bio.isEmpty {
                            Text(bio)
                                .font(.system(size: 14))
                                .foregroundColor(UsColors.textSecondary)
                                .padding(.horizontal, 16)
                        }

                        // Action Buttons: Edit / Share or Follow / Message
                        HStack(spacing: 10) {
                            if isMe {
                                Button(action: { showEditProfile = true }) {
                                    Text("Edit Profile")
                                        .font(.system(size: 14, weight: .bold))
                                        .foregroundColor(UsColors.textPrimary)
                                        .frame(maxWidth: .infinity)
                                        .padding(.vertical, 10)
                                        .background(UsColors.bgSecondary)
                                        .clipShape(RoundedRectangle(cornerRadius: 10))
                                        .overlay(RoundedRectangle(cornerRadius: 10).stroke(UsColors.borderMedium, lineWidth: 1))
                                }

                                Button(action: { showQRModal = true }) {
                                    Text("Share Profile")
                                        .font(.system(size: 14, weight: .bold))
                                        .foregroundColor(UsColors.textPrimary)
                                        .frame(maxWidth: .infinity)
                                        .padding(.vertical, 10)
                                        .background(UsColors.bgSecondary)
                                        .clipShape(RoundedRectangle(cornerRadius: 10))
                                        .overlay(RoundedRectangle(cornerRadius: 10).stroke(UsColors.borderMedium, lineWidth: 1))
                                }
                            } else {
                                Button(action: { viewModel.toggleFollow() }) {
                                    Text(viewModel.isFollowingLocal ? "Following" : "Follow")
                                        .font(.system(size: 14, weight: .bold))
                                        .foregroundColor(viewModel.isFollowingLocal ? UsColors.textPrimary : .black)
                                        .frame(maxWidth: .infinity)
                                        .padding(.vertical, 10)
                                        .background(viewModel.isFollowingLocal ? UsColors.bgSecondary : Color.white)
                                        .clipShape(RoundedRectangle(cornerRadius: 10))
                                        .overlay(RoundedRectangle(cornerRadius: 10).stroke(UsColors.borderMedium, lineWidth: viewModel.isFollowingLocal ? 1 : 0))
                                }

                                Button(action: {
                                    ToastManager.shared.show("Opening Direct Messages...", style: .info)
                                }) {
                                    Text("Message")
                                        .font(.system(size: 14, weight: .bold))
                                        .foregroundColor(UsColors.textPrimary)
                                        .frame(maxWidth: .infinity)
                                        .padding(.vertical, 10)
                                        .background(UsColors.bgSecondary)
                                        .clipShape(RoundedRectangle(cornerRadius: 10))
                                        .overlay(RoundedRectangle(cornerRadius: 10).stroke(UsColors.borderMedium, lineWidth: 1))
                                }
                            }
                        }
                        .padding(.horizontal, 16)

                        Divider()
                            .background(UsColors.borderSubtle)

                        // Posts Grid
                        if viewModel.posts.isEmpty {
                            UsEmptyState(title: "No Posts", detail: "This user hasn't posted anything yet.")
                                .frame(height: 200)
                        } else {
                            LazyVGrid(columns: columns, spacing: 2) {
                                ForEach(viewModel.posts) { post in
                                    gridCell(post)
                                }
                            }
                        }
                    }
                    .padding(.top, 16)
                }
            }
        }
        .navigationTitle(viewModel.profile?.username.map { "@\($0)" } ?? "Profile")
        .navigationBarTitleDisplayMode(.inline)
        .toolbar {
            if isMe {
                ToolbarItem(placement: .topBarTrailing) {
                    NavigationLink(destination: SettingsView(sessionManager: SessionManager.shared)) {
                        Image(systemName: "gearshape.fill")
                            .foregroundColor(UsColors.textPrimary)
                    }
                }
            }
        }
        .sheet(isPresented: $showEditProfile) {
            EditProfileView(currentProfile: viewModel.profile) {
                showEditProfile = false
                Task { await viewModel.loadProfile() }
            }
        }
        .sheet(isPresented: $showQRModal) {
            ProfileQRCodeView(
                username: viewModel.profile?.username ?? "user",
                displayName: viewModel.profile?.displayName ?? "User"
            ) {
                showQRModal = false
            }
        }
        .task {
            if viewModel.profile == nil {
                await viewModel.loadProfile()
            }
        }
    }

    private func profileHeader(_ profile: UserProfile) -> some View {
        HStack(spacing: 20) {
            UsAvatar(
                name: profile.displayName ?? profile.username ?? "User",
                url: profile.avatarUrl,
                size: .large
            )

            HStack(spacing: 24) {
                statItem(count: profile.postsCount, label: "Posts")

                NavigationLink(destination: FollowersListView(userId: profile.id, type: .followers, onOpenAuthor: { _ in })) {
                    statItem(count: profile.followersCount, label: "Followers")
                }
                .buttonStyle(.plain)

                NavigationLink(destination: FollowersListView(userId: profile.id, type: .following, onOpenAuthor: { _ in })) {
                    statItem(count: profile.followingCount, label: "Following")
                }
                .buttonStyle(.plain)
            }
        }
        .padding(.horizontal, 16)
    }

    private func statItem(count: Int, label: String) -> some View {
        VStack(spacing: 4) {
            Text("\(count)")
                .font(.system(size: 17, weight: .bold))
                .foregroundColor(UsColors.textPrimary)
            Text(label)
                .font(.system(size: 12))
                .foregroundColor(UsColors.textMuted)
        }
    }

    @ViewBuilder
    private func gridCell(_ post: FeedItem) -> some View {
        Button(action: { onOpenPost(post.id) }) {
            ZStack {
                if let firstMedia = post.media.first,
                   let posterString = firstMedia.posterUrl,
                   let posterURL = URL(string: posterString) {
                    AsyncImage(url: posterURL) { phase in
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
                    Text(post.text)
                        .font(.system(size: 11))
                        .foregroundColor(UsColors.textMuted)
                        .padding(8)
                }

                if post.media.first?.isVideo == true {
                    VStack {
                        HStack {
                            Spacer()
                            Image(systemName: "play.fill")
                                .font(.system(size: 10))
                                .foregroundColor(.white)
                                .padding(6)
                        }
                        Spacer()
                    }
                }
            }
            .frame(height: 124)
            .clipped()
        }
        .buttonStyle(.plain)
    }
}
