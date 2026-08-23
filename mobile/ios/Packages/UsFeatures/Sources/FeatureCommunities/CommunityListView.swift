import SwiftUI
import UsModel
import UsDesignSystem
import UsNetwork

@Observable
public final class CommunitiesViewModel: @unchecked Sendable {
    public var joinedCommunities: [Community] = []
    public var discoverCommunities: [Community] = []
    public var isLoading: Bool = false

    private let client: APIClientProtocol

    public init(client: APIClientProtocol = APIClient()) {
        self.client = client
        populateMockData()
    }

    @MainActor
    public func loadCommunities() async {
        isLoading = true
        do {
            let res: [Community] = try await client.request(
                endpoint: "v1/communities",
                method: "GET",
                query: nil,
                body: nil
            )
            if !res.isEmpty {
                self.discoverCommunities = res
                self.joinedCommunities = res.filter { $0.isJoined }
            }
        } catch {
            // Keep default initial set
        }
        isLoading = false
    }

    public func toggleJoin(community: Community) {
        if let idx = discoverCommunities.firstIndex(where: { $0.id == community.id }) {
            var updated = discoverCommunities[idx]
            let newState = !updated.isJoined
            updated = Community(
                id: updated.id,
                name: updated.name,
                description: updated.description,
                bannerUrl: updated.bannerUrl,
                iconUrl: updated.iconUrl,
                membersCount: updated.membersCount + (newState ? 1 : -1),
                isJoined: newState,
                category: updated.category
            )
            discoverCommunities[idx] = updated
            if newState {
                joinedCommunities.append(updated)
            } else {
                joinedCommunities.removeAll { $0.id == community.id }
            }
            HapticManager.shared.trigger(.selection)
            ToastManager.shared.show(newState ? "Joined \(community.name)" : "Left \(community.name)", style: .info)

            // Async notify backend
            Task {
                let endpoint = "v1/communities/\(community.id)/\(newState ? "join" : "leave")"
                let _: [String: String]? = try? await client.request(endpoint: endpoint, method: "POST", query: nil, body: nil)
            }
        }
    }

    private func populateMockData() {
        let c1 = Community(id: "c1", name: "Bangalore Food Explorers", description: "Finding the best dosas, filter coffee, and hidden culinary gems in Bengaluru.", bannerUrl: "https://images.unsplash.com/photo-1563379091339-03b21ab4a4f8?w=800", iconUrl: "🍛", membersCount: 18400, isJoined: true, category: "Food")
        let c2 = Community(id: "c2", name: "Swift & iOS Developers India", description: "SwiftUI, Swift 6 concurrency, architecture debates, and career chats.", bannerUrl: "https://images.unsplash.com/photo-1555066931-4365d14bab8c?w=800", iconUrl: "💻", membersCount: 9200, isJoined: true, category: "Tech")
        let c3 = Community(id: "c3", name: "Indie Film & Cinematography", description: "Screenwriting, camera rigs, color grading, and short film reviews.", bannerUrl: "https://images.unsplash.com/photo-1485846234645-a62644f84728?w=800", iconUrl: "🎬", membersCount: 6500, isJoined: false, category: "Creative")
        let c4 = Community(id: "c4", name: "Startups & Product Builders", description: "Founder journeys, growth hacks, and fundraising in India.", bannerUrl: "https://images.unsplash.com/photo-1519389950473-47ba0277781c?w=800", iconUrl: "🚀", membersCount: 24800, isJoined: false, category: "Startups")

        joinedCommunities = [c1, c2]
        discoverCommunities = [c1, c2, c3, c4]
    }
}

public struct CommunityListView: View {
    @State private var viewModel: CommunitiesViewModel
    @State private var selectedCommunity: Community? = nil
    public let onDismiss: () -> Void

    public init(client: APIClientProtocol = APIClient(), onDismiss: @escaping () -> Void = {}) {
        _viewModel = State(initialValue: CommunitiesViewModel(client: client))
        self.onDismiss = onDismiss
    }

    public var body: some View {
        NavigationStack {
            ZStack {
                UsColors.bgPrimary
                    .ignoresSafeArea()

                ScrollView {
                    VStack(alignment: .leading, spacing: 20) {
                        // Your Communities
                        if !viewModel.joinedCommunities.isEmpty {
                            Text("Your Communities")
                                .font(.system(size: 17, weight: .bold))
                                .foregroundColor(UsColors.textPrimary)

                            ScrollView(.horizontal, showsIndicators: false) {
                                HStack(spacing: 12) {
                                    ForEach(viewModel.joinedCommunities) { comm in
                                        Button(action: { selectedCommunity = comm }) {
                                            VStack(spacing: 8) {
                                                Text(comm.iconUrl)
                                                    .font(.system(size: 32))
                                                    .frame(width: 64, height: 64)
                                                    .background(UsColors.bgSecondary)
                                                    .clipShape(Circle())
                                                    .overlay(Circle().stroke(UsColors.borderMedium, lineWidth: 1))

                                                Text(comm.name)
                                                    .font(.system(size: 12, weight: .semibold))
                                                    .foregroundColor(UsColors.textPrimary)
                                                    .lineLimit(1)
                                            }
                                            .frame(width: 80)
                                        }
                                        .buttonStyle(.plain)
                                    }
                                }
                            }
                        }

                        // Discover More
                        Text("Discover Communities")
                            .font(.system(size: 17, weight: .bold))
                            .foregroundColor(UsColors.textPrimary)

                        LazyVStack(spacing: 14) {
                            ForEach(viewModel.discoverCommunities) { comm in
                                communityRow(comm)
                            }
                        }
                    }
                    .padding(16)
                }
            }
            .navigationTitle("Communities")
            .navigationBarTitleDisplayMode(.inline)
            .task {
                await viewModel.loadCommunities()
            }
            .toolbar {
                ToolbarItem(placement: .cancellationAction) {
                    Button("Close", action: onDismiss)
                        .foregroundColor(UsColors.textMuted)
                }
            }
            .sheet(item: $selectedCommunity) { comm in
                CommunityDetailView(community: comm) {
                    selectedCommunity = nil
                }
            }
        }
    }

    @ViewBuilder
    private func communityRow(_ comm: Community) -> some View {
        Button(action: { selectedCommunity = comm }) {
            HStack(spacing: 14) {
                Text(comm.iconUrl)
                    .font(.system(size: 28))
                    .frame(width: 52, height: 52)
                    .background(UsColors.bgTertiary)
                    .clipShape(RoundedRectangle(cornerRadius: 12))

                VStack(alignment: .leading, spacing: 3) {
                    Text(comm.name)
                        .font(.system(size: 15, weight: .bold))
                        .foregroundColor(UsColors.textPrimary)

                    Text("\(comm.membersCount) members • \(comm.category)")
                        .font(.system(size: 12))
                        .foregroundColor(UsColors.textMuted)

                    Text(comm.description)
                        .font(.system(size: 12))
                        .foregroundColor(UsColors.textSecondary)
                        .lineLimit(2)
                }

                Spacer()

                Button(action: { viewModel.toggleJoin(community: comm) }) {
                    Text(comm.isJoined ? "Joined" : "Join")
                        .font(.system(size: 13, weight: .bold))
                        .foregroundColor(comm.isJoined ? UsColors.textPrimary : .black)
                        .padding(.horizontal, 14)
                        .padding(.vertical, 6)
                        .background(comm.isJoined ? UsColors.bgTertiary : Color.white)
                        .clipShape(Capsule())
                }
            }
            .padding(12)
            .background(UsColors.bgSecondary)
            .clipShape(RoundedRectangle(cornerRadius: 14))
        }
        .buttonStyle(.plain)
    }
}

public struct CommunityDetailView: View {
    public let community: Community
    public let onDismiss: () -> Void

    public init(community: Community, onDismiss: @escaping () -> Void = {}) {
        self.community = community
        self.onDismiss = onDismiss
    }

    public var body: some View {
        NavigationStack {
            ZStack {
                UsColors.bgPrimary
                    .ignoresSafeArea()

                ScrollView {
                    VStack(alignment: .leading, spacing: 16) {
                        // Banner with Avatar
                        ZStack(alignment: .bottomLeading) {
                            if let url = URL(string: community.bannerUrl) {
                                AsyncImage(url: url) { phase in
                                    switch phase {
                                    case .success(let img):
                                        img.resizable().scaledToFill()
                                    default:
                                        Rectangle().fill(UsColors.bgTertiary)
                                    }
                                }
                                .frame(height: 140)
                                .clipped()
                            } else {
                                Rectangle().fill(UsColors.bgTertiary).frame(height: 140)
                            }

                            Text(community.iconUrl)
                                .font(.system(size: 38))
                                .frame(width: 68, height: 68)
                                .background(UsColors.bgSecondary)
                                .clipShape(Circle())
                                .overlay(Circle().stroke(UsColors.bgPrimary, lineWidth: 3))
                                .offset(x: 16, y: 34)
                        }

                        // Info
                        VStack(alignment: .leading, spacing: 8) {
                            Text(community.name)
                                .font(.system(size: 20, weight: .bold))
                                .foregroundColor(UsColors.textPrimary)
                                .padding(.top, 24)

                            Text("\(community.membersCount) members • Public Group")
                                .font(.system(size: 13, weight: .medium))
                                .foregroundColor(UsColors.postbookPrimary)

                            Text(community.description)
                                .font(.system(size: 14))
                                .foregroundColor(UsColors.textSecondary)
                                .lineSpacing(3)
                        }
                        .padding(.horizontal, 16)

                        Divider().background(UsColors.borderSubtle)

                        // Community Posts Feed
                        Text("Discussions")
                            .font(.system(size: 17, weight: .bold))
                            .foregroundColor(UsColors.textPrimary)
                            .padding(.horizontal, 16)

                        UsEmptyState(title: "No Discussions Yet", detail: "Be the first to share an update in \(community.name)!")
                    }
                }
            }
            .navigationBarTitleDisplayMode(.inline)
            .toolbar {
                ToolbarItem(placement: .cancellationAction) {
                    Button("Close", action: onDismiss)
                        .foregroundColor(UsColors.textMuted)
                }
            }
        }
    }
}
