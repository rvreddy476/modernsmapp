import SwiftUI
import UsModel
import UsDesignSystem
import UsNetwork
import UsMedia

public struct LiveChatMessage: Identifiable, Sendable {
    public let id = UUID()
    public let username: String
    public let text: String
}

@Observable
public final class LiveStreamViewModel: @unchecked Sendable {
    public var messages: [LiveChatMessage] = []
    public var viewerCount: Int = 1420
    public var heartTrigger: Int = 0
    public var draftMessage: String = ""

    public init() {
        populateMockChat()
    }

    public func sendHeart() {
        heartTrigger += 1
    }

    public func sendMessage() {
        let clean = draftMessage.trimmingCharacters(in: .whitespacesAndNewlines)
        guard !clean.isEmpty else { return }
        messages.append(LiveChatMessage(username: "you", text: clean))
        draftMessage = ""
    }

    private func populateMockChat() {
        messages = [
            LiveChatMessage(username: "alex", text: "Hey everyone! 👋"),
            LiveChatMessage(username: "sarah_c", text: "Quality is amazing today!"),
            LiveChatMessage(username: "marcus_v", text: "Let's goooo 🔥"),
            LiveChatMessage(username: "elena", text: "Can you show the new setup?")
        ]
    }
}

public struct LiveStreamView: View {
    @State private var viewModel = LiveStreamViewModel()
    public let streamTitle: String
    public let broadcaster: Author
    public let onDismiss: () -> Void

    public init(
        streamTitle: String = "Building the Future of Social Apps",
        broadcaster: Author = Author(id: "b1", username: "creator", displayName: "Lead Creator"),
        onDismiss: @escaping () -> Void = {}
    ) {
        self.streamTitle = streamTitle
        self.broadcaster = broadcaster
        self.onDismiss = onDismiss
    }

    public var body: some View {
        ZStack {
            // Live Stream Background Video Canvas
            LinearGradient(
                colors: [Color(red: 0x1A/255.0, green: 0x14/255.0, blue: 0x2A/255.0),
                         Color(red: 0x08/255.0, green: 0x06/255.0, blue: 0x12/255.0)],
                startPoint: .topLeading,
                endPoint: .bottomTrailing
            )
            .ignoresSafeArea()

            // Floating Hearts Particle Canvas
            FloatingHeartsEmitter(trigger: $viewModel.heartTrigger)

            VStack {
                // Top Header: Broadcaster info + LIVE badge + Viewers + Close
                HStack(spacing: 10) {
                    UsAvatar(name: broadcaster.nameForDisplay, url: broadcaster.avatarUrl, size: .medium)

                    VStack(alignment: .leading, spacing: 2) {
                        Text(broadcaster.nameForDisplay)
                            .font(.system(size: 14, weight: .bold))
                            .foregroundColor(.white)
                        Text(streamTitle)
                            .font(.system(size: 11))
                            .foregroundColor(.white.opacity(0.8))
                            .lineLimit(1)
                    }

                    Spacer()

                    // LIVE Pill
                    HStack(spacing: 4) {
                        Circle().fill(Color.white).frame(width: 6, height: 6)
                        Text("LIVE")
                            .font(.system(size: 11, weight: .black))
                            .foregroundColor(.white)
                    }
                    .padding(.horizontal, 8)
                    .padding(.vertical, 4)
                    .background(UsColors.liveRed)
                    .clipShape(Capsule())

                    // Viewers count
                    HStack(spacing: 4) {
                        Image(systemName: "eye.fill")
                            .font(.system(size: 11))
                        Text("\(viewModel.viewerCount)")
                            .font(.system(size: 11, weight: .semibold))
                    }
                    .foregroundColor(.white)
                    .padding(.horizontal, 8)
                    .padding(.vertical, 4)
                    .background(Color.black.opacity(0.4))
                    .clipShape(Capsule())

                    Button(action: onDismiss) {
                        Image(systemName: "xmark")
                            .font(.system(size: 16, weight: .bold))
                            .foregroundColor(.white)
                            .padding(8)
                    }
                }
                .padding(.horizontal, 16)
                .padding(.top, 8)

                Spacer()

                // Bottom: Scrolling Live Chat Overlay + Action Inputs
                VStack(alignment: .leading, spacing: 12) {
                    // Chat Messages
                    ScrollViewReader { proxy in
                        ScrollView(showsIndicators: false) {
                            LazyVStack(alignment: .leading, spacing: 6) {
                                ForEach(viewModel.messages) { msg in
                                    HStack(spacing: 6) {
                                        Text(msg.username)
                                            .font(.system(size: 13, weight: .bold))
                                            .foregroundColor(UsColors.postbookPrimary)
                                        Text(msg.text)
                                            .font(.system(size: 13))
                                            .foregroundColor(.white)
                                    }
                                    .padding(.horizontal, 10)
                                    .padding(.vertical, 4)
                                    .background(Color.black.opacity(0.45))
                                    .clipShape(Capsule())
                                    .id(msg.id)
                                }
                            }
                        }
                        .frame(height: 160)
                    }

                    // Input bar + Reaction Button
                    HStack(spacing: 12) {
                        TextField("Comment live...", text: $viewModel.draftMessage)
                            .textFieldStyle(.plain)
                            .font(.system(size: 14))
                            .foregroundColor(.white)
                            .padding(.horizontal, 16)
                            .padding(.vertical, 10)
                            .background(Color.black.opacity(0.45))
                            .clipShape(Capsule())
                            .overlay(Capsule().stroke(Color.white.opacity(0.2), lineWidth: 1))
                            .onSubmit {
                                viewModel.sendMessage()
                            }

                        Button(action: {
                            viewModel.sendHeart()
                        }) {
                            ZStack {
                                Circle()
                                    .fill(UsColors.postgramPrimary)
                                    .frame(width: 44, height: 44)
                                Image(systemName: "heart.fill")
                                    .font(.system(size: 22))
                                    .foregroundColor(.white)
                            }
                        }
                    }
                }
                .padding(.horizontal, 16)
                .padding(.bottom, 20)
            }
        }
    }
}
