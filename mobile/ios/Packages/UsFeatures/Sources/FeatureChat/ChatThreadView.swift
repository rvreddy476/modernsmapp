import SwiftUI
import UsModel
import UsDesignSystem
import UsNetwork

public struct ChatMessage: Identifiable, Codable, Sendable {
    public let id: String
    public let senderId: String
    public let recipientId: String
    public let text: String
    public let createdAt: String
    public let isMe: Bool

    enum CodingKeys: String, CodingKey {
        case id
        case senderId = "sender_id"
        case recipientId = "recipient_id"
        case text
        case createdAt = "created_at"
        case isMe = "is_me"
    }
}

public struct ChatThread: Identifiable, Codable, Sendable {
    public let id: String
    public let participant: Author
    public let lastMessage: String?
    public let lastMessageTime: String?
    public let unreadCount: Int

    enum CodingKeys: String, CodingKey {
        case id
        case participant
        case lastMessage = "last_message"
        case lastMessageTime = "last_message_time"
        case unreadCount = "unread_count"
    }
}

@Observable
public final class ChatThreadViewModel: @unchecked Sendable {
    public var messages: [ChatMessage] = []
    public var draftText: String = ""
    public var isLoading: Bool = false
    public var isSending: Bool = false

    private let threadId: String
    private let client: APIClientProtocol

    public init(threadId: String, client: APIClientProtocol = APIClient()) {
        self.threadId = threadId
        self.client = client
    }

    @MainActor
    public func loadMessages() async {
        isLoading = true
        do {
            let response: [ChatMessage] = try await client.request(
                endpoint: "v1/chat/threads/\(threadId)/messages",
                method: "GET",
                query: nil,
                body: nil
            )
            self.messages = response
        } catch {
            // Fallback
        }
        self.isLoading = false
    }

    @MainActor
    public func sendMessage() async {
        let cleanText = draftText.trimmingCharacters(in: .whitespacesAndNewlines)
        guard !cleanText.isEmpty else { return }

        isSending = true
        do {
            let body = try JSONEncoder().encode(["text": cleanText])
            let newMessage: ChatMessage = try await client.request(
                endpoint: "v1/chat/threads/\(threadId)/messages",
                method: "POST",
                query: nil,
                body: body
            )
            self.messages.append(newMessage)
            self.draftText = ""
        } catch {
            // Error handling
        }
        self.isSending = false
    }
}

public struct ChatThreadView: View {
    @State private var viewModel: ChatThreadViewModel
    public let participant: Author

    public init(
        threadId: String,
        participant: Author,
        client: APIClientProtocol = APIClient()
    ) {
        self.participant = participant
        _viewModel = State(initialValue: ChatThreadViewModel(threadId: threadId, client: client))
    }

    public var body: some View {
        ZStack {
            UsColors.bgPrimary
                .ignoresSafeArea()

            VStack(spacing: 0) {
                // Messages List
                ScrollViewReader { proxy in
                    ScrollView {
                        LazyVStack(spacing: 12) {
                            ForEach(viewModel.messages) { message in
                                messageBubble(message)
                                    .id(message.id)
                            }
                        }
                        .padding(16)
                    }
                    .onChange(of: viewModel.messages.count) { _, _ in
                        if let lastId = viewModel.messages.last?.id {
                            withAnimation {
                                proxy.scrollTo(lastId, anchor: .bottom)
                            }
                        }
                    }
                }

                // Input Bar
                HStack(spacing: 12) {
                    TextField("Message...", text: $viewModel.draftText)
                        .textFieldStyle(.plain)
                        .font(.system(size: 15))
                        .foregroundColor(UsColors.textPrimary)
                        .padding(.horizontal, 16)
                        .padding(.vertical, 10)
                        .background(UsColors.bgSecondary)
                        .clipShape(Capsule())

                    Button(action: {
                        Task { await viewModel.sendMessage() }
                    }) {
                        Image(systemName: "arrow.up.circle.fill")
                            .font(.system(size: 32))
                            .foregroundColor(
                                viewModel.draftText.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty
                                    ? UsColors.textMuted
                                    : UsColors.postbookPrimary
                            )
                    }
                    .disabled(viewModel.draftText.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty || viewModel.isSending)
                }
                .padding(.horizontal, 16)
                .padding(.vertical, 10)
                .background(UsColors.bgSecondary)
            }
        }
        .navigationTitle(participant.nameForDisplay)
        .navigationBarTitleDisplayMode(.inline)
        .task {
            await viewModel.loadMessages()
        }
    }

    @ViewBuilder
    private func messageBubble(_ message: ChatMessage) -> some View {
        HStack {
            if message.isMe { Spacer() }

            Text(message.text)
                .font(.system(size: 15))
                .foregroundColor(message.isMe ? .white : UsColors.textPrimary)
                .padding(.horizontal, 14)
                .padding(.vertical, 10)
                .background(
                    message.isMe
                        ? UsColors.postbookPrimary
                        : UsColors.bgSecondary
                )
                .clipShape(RoundedRectangle(cornerRadius: 18))

            if !message.isMe { Spacer() }
        }
    }
}
