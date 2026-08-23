import SwiftUI
import UsModel
import UsDesignSystem
import UsNetwork

@Observable
public final class ChatListViewModel: @unchecked Sendable {
    public var threads: [ChatThread] = []
    public var isLoading: Bool = false
    public var errorMessage: String? = nil

    private let client: APIClientProtocol

    public init(client: APIClientProtocol = APIClient()) {
        self.client = client
    }

    @MainActor
    public func loadThreads() async {
        isLoading = true
        errorMessage = nil
        do {
            let response: [ChatThread] = try await client.request(
                endpoint: "v1/chat/threads",
                method: "GET",
                query: nil,
                body: nil
            )
            self.threads = response
        } catch {
            self.errorMessage = error.localizedDescription
        }
        self.isLoading = false
    }
}

public struct ChatListView: View {
    @State private var viewModel: ChatListViewModel
    public let client: APIClientProtocol

    public init(client: APIClientProtocol = APIClient()) {
        self.client = client
        _viewModel = State(initialValue: ChatListViewModel(client: client))
    }

    public var body: some View {
        NavigationStack {
            ZStack {
                UsColors.bgPrimary
                    .ignoresSafeArea()

                if viewModel.isLoading && viewModel.threads.isEmpty {
                    UsLoadingState(message: "Loading messages...")
                } else if let error = viewModel.errorMessage, viewModel.threads.isEmpty {
                    UsErrorState(message: error) {
                        Task { await viewModel.loadThreads() }
                    }
                } else if viewModel.threads.isEmpty {
                    UsEmptyState(title: "No Conversations", detail: "Start a chat with creators and friends.")
                } else {
                    List(viewModel.threads) { thread in
                        NavigationLink(destination: ChatThreadView(threadId: thread.id, participant: thread.participant, client: client)) {
                            threadRow(thread)
                        }
                        .listRowBackground(UsColors.bgPrimary)
                        .listRowSeparatorTint(UsColors.borderSubtle)
                    }
                    .listStyle(.plain)
                    .scrollContentBackground(.hidden)
                }
            }
            .navigationTitle("Messages")
            .task {
                await viewModel.loadThreads()
            }
        }
    }

    private func threadRow(_ thread: ChatThread) -> some View {
        HStack(spacing: 12) {
            UsAvatar(
                name: thread.participant.nameForDisplay,
                url: thread.participant.avatarUrl,
                size: .medium
            )

            VStack(alignment: .leading, spacing: 4) {
                Text(thread.participant.nameForDisplay)
                    .font(.system(size: 15, weight: .semibold))
                    .foregroundColor(UsColors.textPrimary)

                if let lastMsg = thread.lastMessage {
                    Text(lastMsg)
                        .font(.system(size: 13))
                        .foregroundColor(UsColors.textMuted)
                        .lineLimit(1)
                }
            }

            Spacer()

            if let time = thread.lastMessageTime {
                Text(time)
                    .font(.system(size: 11))
                    .foregroundColor(UsColors.textDim)
            }
        }
        .padding(.vertical, 4)
    }
}
