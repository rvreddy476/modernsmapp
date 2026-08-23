import SwiftUI
import UsModel
import UsDesignSystem
import UsNetwork

public struct FeedView<HeaderContent: View>: View {
    @State private var viewModel: FeedViewModel
    @State private var commentsPostId: String? = nil
    @State private var selectedFeedFilter: String = "For You"

    public let onOpenPost: (String) -> Void
    public let onOpenAuthor: (String) -> Void
    public let onOpenNotifications: () -> Void
    public let onOpenChat: () -> Void
    public let onOpenScan: () -> Void
    public let onOpenShortcut: (String) -> Void
    private let headerContent: HeaderContent

    public init(
        client: APIClientProtocol = APIClient(),
        onOpenPost: @escaping (String) -> Void = { _ in },
        onOpenAuthor: @escaping (String) -> Void = { _ in },
        onOpenNotifications: @escaping () -> Void = {},
        onOpenChat: @escaping () -> Void = {},
        onOpenScan: @escaping () -> Void = {},
        onOpenShortcut: @escaping (String) -> Void = { _ in },
        @ViewBuilder header: () -> HeaderContent = { EmptyView() }
    ) {
        _viewModel = State(initialValue: FeedViewModel(client: client))
        self.onOpenPost = onOpenPost
        self.onOpenAuthor = onOpenAuthor
        self.onOpenNotifications = onOpenNotifications
        self.onOpenChat = onOpenChat
        self.onOpenScan = onOpenScan
        self.onOpenShortcut = onOpenShortcut
        self.headerContent = header()
    }

    public var body: some View {
        NavigationStack {
            ZStack {
                UsColors.bgPrimary
                    .ignoresSafeArea()

                VStack(spacing: 0) {
                    // Custom Super-App Top Header Bar
                    topNavigationBar

                    if viewModel.isLoading && viewModel.items.isEmpty {
                        UsLoadingState(message: "Loading your feed...")
                    } else if let error = viewModel.errorMessage, viewModel.items.isEmpty {
                        UsErrorState(message: error) {
                            Task { await viewModel.loadFeed(refresh: true) }
                        }
                    } else if viewModel.items.isEmpty {
                        UsEmptyState(
                            title: "Nothing here yet",
                            detail: "Posts from people you follow will show up here."
                        )
                    } else {
                        feedList
                    }
                }
            }
            .toolbar(.hidden, for: .navigationBar)
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
                if viewModel.items.isEmpty {
                    await viewModel.loadFeed(refresh: true)
                }
            }
        }
    }

    // Top Brand Navigation Header with Action Icons
    private var topNavigationBar: some View {
        HStack(spacing: 16) {
            // Brand Logo with gradient
            HStack(spacing: 6) {
                ZStack {
                    RoundedRectangle(cornerRadius: 10)
                        .fill(
                            LinearGradient(
                                colors: [Color(red: 0x6A/255.0, green: 0x11/255.0, blue: 0xCB/255.0), Color(red: 0x25/255.0, green: 0x75/255.0, blue: 0xFC/255.0)],
                                startPoint: .topLeading,
                                endPoint: .bottomTrailing
                            )
                        )
                        .frame(width: 32, height: 32)

                    Text("US")
                        .font(.system(size: 16, weight: .black, design: .rounded))
                        .foregroundColor(.white)
                }

                Text("US")
                    .font(.system(size: 20, weight: .black, design: .rounded))
                    .foregroundColor(UsColors.textPrimary)
            }

            Spacer()

            // QR & UPI Scanner Button
            Button(action: {
                HapticManager.shared.trigger(.selection)
                onOpenScan()
            }) {
                Image(systemName: "qrcode.viewfinder")
                    .font(.system(size: 20))
                    .foregroundColor(UsColors.textPrimary)
            }
            .buttonStyle(.plain)

            // Notifications Bell with Unread Indicator
            Button(action: {
                HapticManager.shared.trigger(.selection)
                onOpenNotifications()
            }) {
                ZStack(alignment: .topTrailing) {
                    Image(systemName: "bell")
                        .font(.system(size: 20))
                        .foregroundColor(UsColors.textPrimary)

                    Circle()
                        .fill(UsColors.liveRed)
                        .frame(width: 8, height: 8)
                        .offset(x: 2, y: -2)
                }
            }
            .buttonStyle(.plain)

            // Direct Messages (Chat) with Unread Count Badge
            Button(action: {
                HapticManager.shared.trigger(.selection)
                onOpenChat()
            }) {
                ZStack(alignment: .topTrailing) {
                    Image(systemName: "paperplane")
                        .font(.system(size: 20))
                        .foregroundColor(UsColors.textPrimary)

                    Text("3")
                        .font(.system(size: 9, weight: .bold))
                        .foregroundColor(.white)
                        .padding(.horizontal, 4)
                        .padding(.vertical, 2)
                        .background(UsColors.postbookPrimary)
                        .clipShape(Capsule())
                        .offset(x: 6, y: -6)
                }
            }
            .buttonStyle(.plain)
        }
        .padding(.horizontal, 16)
        .padding(.vertical, 10)
        .background(UsColors.bgPrimary)
    }

    // Super-App Quick Actions Strip
    private var quickActionsStrip: some View {
        ScrollView(.horizontal, showsIndicators: false) {
            HStack(spacing: 8) {
                quickChip("⚡️ Scan UPI", action: { onOpenShortcut("wallet") })
                quickChip("🪙 24K Gold", action: { onOpenShortcut("gold") })
                quickChip("🍔 Food 10m", action: { onOpenShortcut("food") })
                quickChip("🚗 Rides", action: { onOpenShortcut("rides") })
                quickChip("🌴 Split Bill", action: { onOpenShortcut("split") })
                quickChip("🎫 Events", action: { onOpenShortcut("events") })
                quickChip("🎧 Spaces", action: { onOpenShortcut("spaces") })
            }
            .padding(.horizontal, 16)
            .padding(.vertical, 6)
        }
    }

    @ViewBuilder
    private func quickChip(_ title: String, action: @escaping () -> Void) -> some View {
        Button(action: {
            HapticManager.shared.trigger(.selection)
            action()
        }) {
            Text(title)
                .font(.system(size: 12, weight: .semibold))
                .foregroundColor(UsColors.textPrimary)
                .padding(.horizontal, 12)
                .padding(.vertical, 6)
                .background(UsColors.bgSecondary)
                .clipShape(Capsule())
                .overlay(Capsule().stroke(UsColors.borderSubtle, lineWidth: 1))
        }
        .buttonStyle(.plain)
    }

    private var feedList: some View {
        ScrollView {
            LazyVStack(spacing: 0) {
                // Stories tray
                headerContent

                // Super-App Quick Action Chips
                quickActionsStrip
                    .padding(.bottom, 6)

                // Feed Items
                ForEach(viewModel.items) { item in
                    let overlay = viewModel.overlays[item.id] ?? EngagementOverlay()
                    PostCardView(
                        item: item,
                        overlay: overlay,
                        onClick: { onOpenPost(item.id) },
                        onAuthorClick: { onOpenAuthor(item.author.id) },
                        onReact: { viewModel.onReact(postId: item.id, serverReacted: item.viewer.hasReacted) },
                        onComment: { commentsPostId = item.id },
                        onRepost: { viewModel.onRepost(postId: item.id, serverReposted: item.viewer.hasReposted) },
                        onBookmark: { viewModel.onBookmark(postId: item.id, serverBookmarked: item.viewer.isBookmarked) },
                        onShare: { sharePost(item) },
                        onTip: {
                            HapticManager.shared.trigger(.success)
                            ToastManager.shared.show("Tipped ₹50 to @\(item.author.username) via UPI ☕️", style: .success)
                        }
                    )
                    .onAppear {
                        if item.id == viewModel.items.last?.id && viewModel.nextCursor != nil {
                            Task { await viewModel.loadFeed(refresh: false) }
                        }
                    }
                }

                if viewModel.isAppending {
                    ProgressView()
                        .tint(UsColors.postbookPrimary)
                        .padding(.vertical, 24)
                }
            }
            .padding(.top, 4)
        }
        .refreshable {
            await viewModel.loadFeed(refresh: true)
        }
    }

    private func sharePost(_ item: FeedItem) {
        #if os(iOS)
        let activityVC = UIActivityViewController(
            activityItems: [item.text, item.author.nameForDisplay],
            applicationActivities: nil
        )
        if let windowScene = UIApplication.shared.connectedScenes.first as? UIWindowScene,
           let rootVC = windowScene.windows.first?.rootViewController {
            rootVC.present(activityVC, animated: true)
        }
        #endif
    }
}

private struct IdentifiableString: Identifiable {
    let id: String
}
