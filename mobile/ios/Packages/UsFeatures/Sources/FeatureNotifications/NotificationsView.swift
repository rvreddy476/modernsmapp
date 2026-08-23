import SwiftUI
import UsModel
import UsDesignSystem
import UsNetwork

public struct UnreadCountResponse: Codable, Sendable {
    public let count: Int
}

public struct MarkReadPayload: Codable, Sendable {
    public let bucket: Int
    public let ts: String

    public init(bucket: Int, ts: String) {
        self.bucket = bucket
        self.ts = ts
    }
}

@Observable
public final class NotificationsViewModel: @unchecked Sendable {
    public var notifications: [NotificationItem] = []
    public var unreadCount: Int = 0
    public var isLoading: Bool = false
    public var errorMessage: String? = nil

    private let client: APIClientProtocol

    public init(client: APIClientProtocol = APIClient()) {
        self.client = client
    }

    @MainActor
    public func loadNotifications() async {
        isLoading = true
        errorMessage = nil
        do {
            async let notifsTask: ApiEnvelope<[NotificationItem]> = client.requestEnvelope(
                endpoint: "v1/notifications",
                method: "GET",
                query: nil,
                body: nil
            )
            async let unreadTask: ApiEnvelope<UnreadCountResponse> = client.requestEnvelope(
                endpoint: "v1/notifications/unread-count",
                method: "GET",
                query: nil,
                body: nil
            )

            let (notifsEnvelope, unreadEnvelope) = try await (notifsTask, unreadTask)
            self.notifications = notifsEnvelope.data ?? []
            self.unreadCount = unreadEnvelope.data?.count ?? 0
        } catch {
            self.errorMessage = error.localizedDescription
        }
        self.isLoading = false
    }

    @MainActor
    public func refreshUnreadCount() async {
        do {
            let response: ApiEnvelope<UnreadCountResponse> = try await client.requestEnvelope(
                endpoint: "v1/notifications/unread-count",
                method: "GET",
                query: nil,
                body: nil
            )
            self.unreadCount = response.data?.count ?? 0
        } catch {
            // Keep existing count on transient error
        }
    }

    public func markAsRead(item: NotificationItem) {
        guard let idx = notifications.firstIndex(where: { $0.bucket == item.bucket && $0.ts == item.ts }) else { return }
        if notifications[idx].isRead { return }

        // Optimistic update
        notifications[idx].isRead = true
        if unreadCount > 0 {
            unreadCount -= 1
        }

        Task {
            let payload = MarkReadPayload(bucket: item.bucket, ts: item.ts)
            do {
                let body = try JSONEncoder().encode(payload)
                let _: EmptyData = try await client.request(
                    endpoint: "v1/notifications/read",
                    method: "POST",
                    query: nil,
                    body: body
                )
            } catch {
                // Rollback on network failure
                await MainActor.run {
                    if let revertIdx = self.notifications.firstIndex(where: { $0.bucket == item.bucket && $0.ts == item.ts }) {
                        self.notifications[revertIdx].isRead = false
                        self.unreadCount += 1
                    }
                }
            }
        }
    }

    @MainActor
    public func markAllAsRead() async {
        let previous = notifications
        let prevCount = unreadCount

        for i in 0..<notifications.count {
            notifications[i].isRead = true
        }
        unreadCount = 0

        do {
            let _: EmptyData = try await client.request(
                endpoint: "v1/notifications/read-all",
                method: "PATCH",
                query: nil,
                body: nil
            )
        } catch {
            self.notifications = previous
            self.unreadCount = prevCount
        }
    }
}

public struct NotificationsView: View {
    @State private var viewModel: NotificationsViewModel
    public let onOpenPost: (String) -> Void
    public let onOpenAuthor: (String) -> Void

    public init(
        client: APIClientProtocol = APIClient(),
        onOpenPost: @escaping (String) -> Void = { _ in },
        onOpenAuthor: @escaping (String) -> Void = { _ in }
    ) {
        _viewModel = State(initialValue: NotificationsViewModel(client: client))
        self.onOpenPost = onOpenPost
        self.onOpenAuthor = onOpenAuthor
    }

    public var body: some View {
        NavigationStack {
            ZStack {
                UsColors.bgPrimary
                    .ignoresSafeArea()

                if viewModel.isLoading && viewModel.notifications.isEmpty {
                    UsLoadingState(message: "Loading activity...")
                } else if let error = viewModel.errorMessage, viewModel.notifications.isEmpty {
                    UsErrorState(message: error) {
                        Task { await viewModel.loadNotifications() }
                    }
                } else if viewModel.notifications.isEmpty {
                    UsEmptyState(
                        title: "No Activity",
                        detail: "When someone likes, comments or follows you, you'll see it here."
                    )
                } else {
                    List(viewModel.notifications) { item in
                        notificationRow(item)
                            .listRowBackground(item.isRead ? UsColors.bgPrimary : UsColors.bgSecondary)
                            .listRowSeparatorTint(UsColors.borderSubtle)
                    }
                    .listStyle(.plain)
                    .scrollContentBackground(.hidden)
                    .refreshable {
                        await viewModel.loadNotifications()
                    }
                }
            }
            .navigationTitle("Activity")
            .navigationBarTitleDisplayMode(.inline)
            .toolbar {
                if !viewModel.notifications.isEmpty {
                    ToolbarItem(placement: .topBarTrailing) {
                        Button("Mark all read") {
                            Task { await viewModel.markAllAsRead() }
                        }
                        .font(.system(size: 13, weight: .medium))
                        .foregroundColor(UsColors.postbookPrimary)
                        .accessibilityLabel("Mark all notifications as read")
                    }
                }
            }
            .task {
                await viewModel.loadNotifications()
            }
        }
    }

    @ViewBuilder
    private func notificationRow(_ item: NotificationItem) -> some View {
        Button(action: {
            viewModel.markAsRead(item: item)
            if let entityId = item.entityId, !entityId.isEmpty {
                onOpenPost(entityId)
            } else if let authorId = item.actorUserId, !authorId.isEmpty {
                onOpenAuthor(authorId)
            }
        }) {
            HStack(spacing: 14) {
                // Actor Avatar or Default Icon + Type Badge
                ZStack(alignment: .bottomTrailing) {
                    if let actor = item.actor {
                        UsAvatar(
                            name: actor.nameForDisplay,
                            url: actor.avatarUrl,
                            size: .medium
                        )
                    } else {
                        Circle()
                            .fill(UsColors.bgTertiary)
                            .frame(width: 40, height: 40)
                            .overlay(
                                Image(systemName: "bell.fill")
                                    .font(.system(size: 16))
                                    .foregroundColor(UsColors.textMuted)
                            )
                    }

                    typeBadge(item.type)
                        .offset(x: 4, y: 4)
                }

                // Text Description
                VStack(alignment: .leading, spacing: 3) {
                    Text(item.displayTitle)
                        .font(.system(size: 14, weight: item.isRead ? .regular : .semibold))
                        .foregroundColor(UsColors.textPrimary)

                    if let comment = item.commentText, !comment.isEmpty {
                        Text("\"\(comment)\"")
                            .font(.system(size: 13))
                            .foregroundColor(UsColors.textMuted)
                            .lineLimit(1)
                    }

                    if let created = item.createdAt {
                        Text(created)
                            .font(.system(size: 11))
                            .foregroundColor(UsColors.textDim)
                    }
                }

                Spacer()

                if !item.isRead {
                    Circle()
                        .fill(UsColors.postbookPrimary)
                        .frame(width: 8, height: 8)
                        .accessibilityLabel("Unread notification indicator")
                }
            }
            .padding(.vertical, 4)
        }
        .buttonStyle(.plain)
        .accessibilityLabel("\(item.displayTitle), \(item.isRead ? "Read" : "Unread")")
    }

    @ViewBuilder
    private func typeBadge(_ type: String) -> some View {
        ZStack {
            Circle()
                .fill(badgeColor(type))
                .frame(width: 18, height: 18)
            Image(systemName: badgeIcon(type))
                .font(.system(size: 9, weight: .bold))
                .foregroundColor(.white)
        }
    }

    private func badgeColor(_ type: String) -> Color {
        switch type {
        case "like", "reaction": return UsColors.postgramPrimary
        case "comment": return UsColors.posttubePrimary
        case "follow", "user_followed": return UsColors.postbookPrimary
        case "mention": return UsColors.postgramSecondary
        case "repost", "post_reposted": return UsColors.onlineGreen
        default: return UsColors.postbookPrimary
        }
    }

    private func badgeIcon(_ type: String) -> String {
        switch type {
        case "like", "reaction": return "heart.fill"
        case "comment": return "bubble.right.fill"
        case "follow", "user_followed": return "person.badge.plus.fill"
        case "mention": return "at"
        case "repost", "post_reposted": return "arrow.2.squarepath"
        default: return "bell.fill"
        }
    }
}
