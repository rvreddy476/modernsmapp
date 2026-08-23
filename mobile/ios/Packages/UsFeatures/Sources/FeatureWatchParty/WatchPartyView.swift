import SwiftUI
import UsModel
import UsDesignSystem

public struct WatchPartyFriend: Identifiable {
    public let id: String
    public let name: String
    public let isHost: Bool

    public init(id: String, name: String, isHost: Bool = false) {
        self.id = id
        self.name = name
        self.isHost = isHost
    }
}

public struct WatchPartyView: View {
    public let videoTitle: String
    public let onDismiss: () -> Void

    @State private var friends: [WatchPartyFriend] = [
        WatchPartyFriend(id: "wp-1", name: "Alex Rivera", isHost: true),
        WatchPartyFriend(id: "wp-2", name: "Sarah Chen"),
        WatchPartyFriend(id: "wp-3", name: "Marcus Vance"),
        WatchPartyFriend(id: "wp-4", name: "Aanya Sharma")
    ]
    @State private var isPlaying: Bool = true
    @State private var chatLog: [String] = [
        "Alex: Welcome to the watch party guys! 🍿",
        "Sarah: This 4K stream looks insane 🔥",
        "Marcus: Wait pause at 04:20! 😂"
    ]
    @State private var chatInput: String = ""

    public init(
        videoTitle: String = "Building India's #1 Super-App in 2026 🇮🇳 (4K Master)",
        onDismiss: @escaping () -> Void = {}
    ) {
        self.videoTitle = videoTitle
        self.onDismiss = onDismiss
    }

    public var body: some View {
        ZStack {
            Color.black.ignoresSafeArea()

            VStack(spacing: 0) {
                // Top Video Screen
                ZStack(alignment: .bottomLeading) {
                    Rectangle()
                        .fill(Color(red: 0x14/255.0, green: 0x14/255.0, blue: 0x22/255.0))

                    VStack(alignment: .leading, spacing: 4) {
                        Text(videoTitle)
                            .font(.system(size: 14, weight: .bold))
                            .foregroundColor(.white)
                            .lineLimit(1)

                        HStack(spacing: 6) {
                            Circle().fill(UsColors.onlineGreen).frame(width: 8, height: 8)
                            Text("Synced Playback • \(friends.count) Friends Watching")
                                .font(.system(size: 11))
                                .foregroundColor(.white.opacity(0.8))
                        }
                    }
                    .padding(14)
                }
                .frame(height: 240)

                // Friends Avatars Ribbon
                ScrollView(.horizontal, showsIndicators: false) {
                    HStack(spacing: 12) {
                        ForEach(friends) { friend in
                            VStack(spacing: 4) {
                                UsAvatar(name: friend.name, size: .medium)
                                    .overlay(
                                        friend.isHost ?
                                            Circle().stroke(Color.yellow, lineWidth: 2) :
                                            Circle().stroke(UsColors.onlineGreen, lineWidth: 2)
                                    )

                                Text(friend.name.split(separator: " ").first ?? "")
                                    .font(.system(size: 11, weight: .semibold))
                                    .foregroundColor(.white)
                            }
                        }
                    }
                    .padding(.horizontal, 16)
                    .padding(.vertical, 12)
                }
                .background(Color(red: 0x10/255.0, green: 0x10/255.0, blue: 0x18/255.0))

                // Watch Party Live Chat
                ScrollView {
                    LazyVStack(alignment: .leading, spacing: 8) {
                        ForEach(chatLog, id: \.self) { msg in
                            Text(msg)
                                .font(.system(size: 13))
                                .foregroundColor(.white)
                                .padding(.horizontal, 12)
                                .padding(.vertical, 6)
                                .background(Color.white.opacity(0.1))
                                .clipShape(RoundedRectangle(cornerRadius: 10))
                        }
                    }
                    .padding(16)
                }

                // Chat Input & Reactions
                HStack(spacing: 8) {
                    TextField("Chat with room...", text: $chatInput)
                        .textFieldStyle(.plain)
                        .font(.system(size: 13))
                        .foregroundColor(.white)
                        .padding(.horizontal, 12)
                        .padding(.vertical, 10)
                        .background(Color.white.opacity(0.15))
                        .clipShape(Capsule())
                        .onSubmit {
                            if !chatInput.isEmpty {
                                chatLog.append("You: \(chatInput)")
                                chatInput = ""
                            }
                        }

                    Button("🍿") {
                        HapticManager.shared.trigger(.light)
                        ToastManager.shared.show("🍿 Popped popcorn!", style: .info)
                    }
                    .font(.system(size: 22))

                    Button("🔥") {
                        HapticManager.shared.trigger(.light)
                        ToastManager.shared.show("🔥 Synced hype!", style: .info)
                    }
                    .font(.system(size: 22))
                }
                .padding(.horizontal, 16)
                .padding(.vertical, 12)
                .background(Color.black)
            }

            // Close button on top
            VStack {
                HStack {
                    Spacer()
                    Button(action: onDismiss) {
                        Image(systemName: "xmark")
                            .font(.system(size: 16, weight: .bold))
                            .foregroundColor(.white)
                            .padding(8)
                            .background(Color.black.opacity(0.6))
                            .clipShape(Circle())
                    }
                }
                .padding(16)
                Spacer()
            }
        }
    }
}
