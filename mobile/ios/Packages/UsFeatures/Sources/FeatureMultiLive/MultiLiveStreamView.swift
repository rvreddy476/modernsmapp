import SwiftUI
import UsModel
import UsDesignSystem
import UsNetwork

public struct MultiLiveStreamView: View {
    public let host: Author
    public let guest: Author
    public let streamTitle: String
    public let onLeave: () -> Void

    @State private var chatMessages: [String] = [
        "Welcome to the dual stream! 🔥",
        "Love this collaboration 🙌",
        "Audio quality is super crisp 🎧",
        "Say hi to Bangalore! 🚀"
    ]
    @State private var newChatText: String = ""

    public init(
        host: Author = Author(id: "h1", username: "sarah_c", displayName: "Sarah Chen"),
        guest: Author = Author(id: "g1", username: "alex_r", displayName: "Alex Rivera"),
        streamTitle: String = "Building India's #1 Social Super-App 🎙️",
        onLeave: @escaping () -> Void = {}
    ) {
        self.host = host
        self.guest = guest
        self.streamTitle = streamTitle
        self.onLeave = onLeave
    }

    public var body: some View {
        ZStack {
            Color.black.ignoresSafeArea()

            VStack(spacing: 4) {
                // Top Broadcaster (Host)
                ZStack(alignment: .topLeading) {
                    Rectangle()
                        .fill(Color(red: 0x1A/255.0, green: 0x1E/255.0, blue: 0x2E/255.0))

                    VStack(spacing: 8) {
                        UsAvatar(name: host.nameForDisplay, url: host.avatarUrl, size: .medium)
                        Text(host.nameForDisplay)
                            .font(.system(size: 13, weight: .bold))
                            .foregroundColor(.white)
                    }
                    .frame(maxWidth: .infinity, maxHeight: .infinity)

                    // Host Badge
                    Text("HOST")
                        .font(.system(size: 10, weight: .black))
                        .foregroundColor(.black)
                        .padding(.horizontal, 8)
                        .padding(.vertical, 4)
                        .background(Color.yellow)
                        .clipShape(Capsule())
                        .padding(12)
                }
                .clipShape(RoundedRectangle(cornerRadius: 16))

                // Bottom Broadcaster (Guest)
                ZStack(alignment: .topLeading) {
                    Rectangle()
                        .fill(Color(red: 0x22/255.0, green: 0x1A/255.0, blue: 0x2E/255.0))

                    VStack(spacing: 8) {
                        UsAvatar(name: guest.nameForDisplay, url: guest.avatarUrl, size: .medium)
                        Text(guest.nameForDisplay)
                            .font(.system(size: 13, weight: .bold))
                            .foregroundColor(.white)
                    }
                    .frame(maxWidth: .infinity, maxHeight: .infinity)

                    // Guest Badge
                    Text("GUEST")
                        .font(.system(size: 10, weight: .black))
                        .foregroundColor(.white)
                        .padding(.horizontal, 8)
                        .padding(.vertical, 4)
                        .background(UsColors.postgramPrimary)
                        .clipShape(Capsule())
                        .padding(12)
                }
                .clipShape(RoundedRectangle(cornerRadius: 16))
            }
            .padding(.horizontal, 8)
            .padding(.top, 44)
            .padding(.bottom, 90)

            // Overlaid Live Controls & Chat
            VStack {
                // Top Header (Live Pill, Viewers, Exit)
                HStack(spacing: 10) {
                    HStack(spacing: 6) {
                        Circle().fill(Color.red).frame(width: 8, height: 8)
                        Text("CO-LIVE")
                            .font(.system(size: 11, weight: .black))
                            .foregroundColor(.white)
                    }
                    .padding(.horizontal, 10)
                    .padding(.vertical, 5)
                    .background(Color.red.opacity(0.8))
                    .clipShape(Capsule())

                    Text("1.8K Watching")
                        .font(.system(size: 12, weight: .semibold))
                        .foregroundColor(.white)
                        .padding(.horizontal, 10)
                        .padding(.vertical, 5)
                        .background(Color.black.opacity(0.5))
                        .clipShape(Capsule())

                    Spacer()

                    Button(action: onLeave) {
                        Image(systemName: "xmark")
                            .font(.system(size: 16, weight: .bold))
                            .foregroundColor(.white)
                            .padding(10)
                            .background(Color.black.opacity(0.5))
                            .clipShape(Circle())
                    }
                }
                .padding(.horizontal, 16)
                .padding(.top, 8)

                Spacer()

                // Chat Stream Overlay
                VStack(alignment: .leading, spacing: 6) {
                    ForEach(chatMessages.suffix(3), id: \.self) { msg in
                        Text(msg)
                            .font(.system(size: 12, weight: .medium))
                            .foregroundColor(.white)
                            .padding(.horizontal, 10)
                            .padding(.vertical, 5)
                            .background(Color.black.opacity(0.6))
                            .clipShape(RoundedRectangle(cornerRadius: 8))
                    }
                }
                .frame(maxWidth: .infinity, alignment: .leading)
                .padding(.horizontal, 16)
                .padding(.bottom, 8)

                // Chat Input Bar
                HStack(spacing: 8) {
                    TextField("Say something...", text: $newChatText)
                        .textFieldStyle(.plain)
                        .font(.system(size: 13))
                        .foregroundColor(.white)
                        .padding(.horizontal, 12)
                        .padding(.vertical, 8)
                        .background(Color.black.opacity(0.6))
                        .clipShape(Capsule())
                        .onSubmit {
                            if !newChatText.isEmpty {
                                chatMessages.append(newChatText)
                                newChatText = ""
                            }
                        }

                    Button("❤️") {
                        HapticManager.shared.trigger(.light)
                    }
                    .font(.system(size: 24))
                }
                .padding(.horizontal, 16)
                .padding(.bottom, 16)
            }
        }
    }
}
