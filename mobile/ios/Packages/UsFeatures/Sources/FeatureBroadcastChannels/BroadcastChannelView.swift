import SwiftUI
import UsModel
import UsDesignSystem
import UsNetwork

@Observable
public final class BroadcastChannelViewModel: @unchecked Sendable {
    public var channel: BroadcastChannel
    public var updates: [BroadcastMessage] = []
    public var reactedMessageIds: Set<String> = []

    public init(channel: BroadcastChannel? = nil) {
        let creator = Author(id: "c1", username: "sarah_c", displayName: "Sarah Chen")
        self.channel = channel ?? BroadcastChannel(id: "bc1", name: "Sarah's Creative Vault 🎨", creator: creator)
        populateMockUpdates()
    }

    public func toggleReaction(id: String) {
        if reactedMessageIds.contains(id) {
            reactedMessageIds.remove(id)
        } else {
            reactedMessageIds.insert(id)
            HapticManager.shared.trigger(.light)
        }
    }

    private func populateMockUpdates() {
        updates = [
            BroadcastMessage(text: "Hey everyone! Working on a brand new long-form design tutorial for PostTube. Dropping the teaser here first! ✨", timestamp: "Yesterday, 6:30 PM"),
            BroadcastMessage(text: "Quick voice note on how I set up lighting for mobile studio shoots 🎙️", voiceDurationSeconds: 42, reactionsCount: 240, timestamp: "Today, 10:15 AM"),
            BroadcastMessage(text: "Here is the color palette I used for the new project 👇", mediaUrl: "https://images.unsplash.com/photo-1507003211169-0a1dd7228f2d?w=800", reactionsCount: 480, timestamp: "2h ago")
        ]
    }
}

public struct BroadcastChannelView: View {
    @State private var viewModel: BroadcastChannelViewModel
    public let onDismiss: () -> Void

    public init(channel: BroadcastChannel? = nil, onDismiss: @escaping () -> Void = {}) {
        _viewModel = State(initialValue: BroadcastChannelViewModel(channel: channel))
        self.onDismiss = onDismiss
    }

    public var body: some View {
        NavigationStack {
            ZStack {
                UsColors.bgPrimary
                    .ignoresSafeArea()

                VStack(spacing: 0) {
                    // Channel Header Info
                    channelHeader

                    // Updates Feed
                    ScrollView {
                        LazyVStack(spacing: 16) {
                            ForEach(viewModel.updates) { msg in
                                updateBubble(msg)
                            }
                        }
                        .padding(16)
                    }

                    // Bottom info bar (Broadcast only host can send)
                    HStack {
                        Image(systemName: "lock.fill")
                            .font(.system(size: 12))
                        Text("Only \(viewModel.channel.creator.nameForDisplay) can send messages")
                            .font(.system(size: 12))
                    }
                    .foregroundColor(UsColors.textMuted)
                    .frame(maxWidth: .infinity)
                    .padding(14)
                    .background(UsColors.bgSecondary)
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

    private var channelHeader: some View {
        HStack(spacing: 12) {
            UsAvatar(name: viewModel.channel.creator.nameForDisplay, url: viewModel.channel.creator.avatarUrl, size: .medium)
            VStack(alignment: .leading, spacing: 2) {
                Text(viewModel.channel.name)
                    .font(.system(size: 15, weight: .bold))
                    .foregroundColor(UsColors.textPrimary)
                Text("\(viewModel.channel.membersCount) members • Broadcast Channel")
                    .font(.system(size: 12))
                    .foregroundColor(UsColors.textMuted)
            }
            Spacer()
        }
        .padding(16)
        .background(UsColors.bgSecondary)
    }

    @ViewBuilder
    private func updateBubble(_ msg: BroadcastMessage) -> some View {
        VStack(alignment: .leading, spacing: 10) {
            Text(msg.text)
                .font(.system(size: 15))
                .foregroundColor(UsColors.textPrimary)
                .lineSpacing(3)

            // Media attachment if any
            if let urlStr = msg.mediaUrl, let url = URL(string: urlStr) {
                AsyncImage(url: url) { phase in
                    switch phase {
                    case .success(let img):
                        img.resizable().scaledToFill()
                    default:
                        Rectangle().fill(UsColors.bgTertiary)
                    }
                }
                .frame(height: 180)
                .clipShape(RoundedRectangle(cornerRadius: 12))
            }

            // Voice clip if any
            if let duration = msg.voiceDurationSeconds {
                HStack(spacing: 10) {
                    Image(systemName: "play.circle.fill")
                        .font(.system(size: 28))
                        .foregroundColor(UsColors.postbookPrimary)

                    Capsule()
                        .fill(UsColors.postbookPrimary.opacity(0.3))
                        .frame(height: 4)

                    Text("0:\(duration)")
                        .font(.system(size: 12, weight: .medium, design: .monospaced))
                        .foregroundColor(UsColors.textMuted)
                }
                .padding(10)
                .background(UsColors.bgTertiary)
                .clipShape(RoundedRectangle(cornerRadius: 10))
            }

            // Footer: Timestamp + Reaction Pill
            HStack {
                Text(msg.timestamp)
                    .font(.system(size: 11))
                    .foregroundColor(UsColors.textDim)

                Spacer()

                Button(action: { viewModel.toggleReaction(id: msg.id) }) {
                    let reacted = viewModel.reactedMessageIds.contains(msg.id)
                    HStack(spacing: 4) {
                        Text("❤️")
                        Text("\(msg.reactionsCount + (reacted ? 1 : 0))")
                            .font(.system(size: 12, weight: .bold))
                            .foregroundColor(reacted ? UsColors.postgramPrimary : UsColors.textPrimary)
                    }
                    .padding(.horizontal, 8)
                    .padding(.vertical, 4)
                    .background(UsColors.bgTertiary)
                    .clipShape(Capsule())
                }
                .buttonStyle(.plain)
            }
        }
        .padding(14)
        .background(UsColors.bgSecondary)
        .clipShape(RoundedRectangle(cornerRadius: 16))
    }
}
