import SwiftUI
import UsModel
import UsDesignSystem
import UsNetwork

@Observable
public final class AudioSpaceViewModel: @unchecked Sendable {
    public var space: AudioSpace
    public var isMuted: Bool = true
    public var isHandRaised: Bool = false
    public var reactionEmoji: String? = nil

    public init(space: AudioSpace? = nil) {
        let host = Speaker(id: "h1", name: "Vikram Malhotra", isSpeaking: true, isHost: true)
        let s1 = Speaker(id: "s1", name: "Ananya Iyer", isSpeaking: false, isMuted: false)
        let s2 = Speaker(id: "s2", name: "Dev Patel", isSpeaking: false, isMuted: true)

        self.space = space ?? AudioSpace(
            id: "sp1",
            title: "Future of Super-Apps & Creator Economy in India 🎙️",
            host: host,
            speakers: [host, s1, s2],
            listenersCount: 480,
            topic: "Tech & Product"
        )
    }

    public func toggleHand() {
        isHandRaised.toggle()
        HapticManager.shared.trigger(.selection)
        ToastManager.shared.show(isHandRaised ? "Hand Raised" : "Hand Lowered", style: .info)
    }

    public func sendReaction(_ emoji: String) {
        reactionEmoji = emoji
        HapticManager.shared.trigger(.light)
        DispatchQueue.main.asyncAfter(deadline: .now() + 2.0) { [weak self] in
            self?.reactionEmoji = nil
        }
    }
}

public struct AudioSpaceView: View {
    @State private var viewModel: AudioSpaceViewModel
    public let onDismiss: () -> Void

    public init(space: AudioSpace? = nil, onDismiss: @escaping () -> Void = {}) {
        _viewModel = State(initialValue: AudioSpaceViewModel(space: space))
        self.onDismiss = onDismiss
    }

    private let speakerColumns = [
        GridItem(.flexible(), spacing: 16),
        GridItem(.flexible(), spacing: 16),
        GridItem(.flexible(), spacing: 16)
    ]

    private let listenerColumns = [
        GridItem(.flexible(), spacing: 12),
        GridItem(.flexible(), spacing: 12),
        GridItem(.flexible(), spacing: 12),
        GridItem(.flexible(), spacing: 12),
        GridItem(.flexible(), spacing: 12)
    ]

    public var body: some View {
        NavigationStack {
            ZStack {
                UsColors.bgPrimary
                    .ignoresSafeArea()

                VStack(spacing: 20) {
                    // Header Topic & Live Pill
                    VStack(spacing: 6) {
                        HStack(spacing: 6) {
                            Circle().fill(UsColors.liveRed).frame(width: 8, height: 8)
                            Text("LIVE AUDIO SPACE")
                                .font(.system(size: 11, weight: .black))
                                .foregroundColor(UsColors.liveRed)
                            Spacer()
                            Text("\(viewModel.space.listenersCount) Listening")
                                .font(.system(size: 12, weight: .semibold))
                                .foregroundColor(UsColors.textMuted)
                        }

                        Text(viewModel.space.title)
                            .font(.system(size: 18, weight: .bold))
                            .foregroundColor(UsColors.textPrimary)
                            .lineSpacing(2)
                            .frame(maxWidth: .infinity, alignment: .leading)
                    }
                    .padding(.horizontal, 16)
                    .padding(.top, 12)

                    ScrollView {
                        VStack(alignment: .leading, spacing: 20) {
                            // Speakers Section
                            Text("Speakers")
                                .font(.system(size: 14, weight: .bold))
                                .foregroundColor(UsColors.textMuted)

                            LazyVGrid(columns: speakerColumns, spacing: 18) {
                                ForEach(viewModel.space.speakers) { speaker in
                                    speakerCell(speaker)
                                }
                            }

                            Divider().background(UsColors.borderSubtle)

                            // Listeners Section
                            Text("Listeners (\(viewModel.space.listenersCount))")
                                .font(.system(size: 14, weight: .bold))
                                .foregroundColor(UsColors.textMuted)

                            LazyVGrid(columns: listenerColumns, spacing: 14) {
                                ForEach(0..<15, id: \.self) { idx in
                                    VStack(spacing: 4) {
                                        UsAvatar(name: "User \(idx + 1)", size: .small)
                                        Text("User \(idx + 1)")
                                            .font(.system(size: 10))
                                            .foregroundColor(UsColors.textDim)
                                            .lineLimit(1)
                                    }
                                }
                            }
                        }
                        .padding(16)
                    }

                    // Bottom Control Bar
                    HStack(spacing: 16) {
                        Button(action: { viewModel.isMuted.toggle() }) {
                            ZStack {
                                Circle()
                                    .fill(viewModel.isMuted ? UsColors.bgSecondary : UsColors.onlineGreen)
                                    .frame(width: 50, height: 50)
                                    .overlay(Circle().stroke(UsColors.borderMedium, lineWidth: 1))

                                Image(systemName: viewModel.isMuted ? "mic.slash.fill" : "mic.fill")
                                    .font(.system(size: 20))
                                    .foregroundColor(viewModel.isMuted ? UsColors.textPrimary : .white)
                            }
                        }

                        Button(action: { viewModel.toggleHand() }) {
                            HStack(spacing: 6) {
                                Image(systemName: "hand.raised.fill")
                                Text(viewModel.isHandRaised ? "Hand Raised" : "Raise Hand")
                            }
                            .font(.system(size: 13, weight: .bold))
                            .foregroundColor(viewModel.isHandRaised ? .black : UsColors.textPrimary)
                            .padding(.horizontal, 16)
                            .padding(.vertical, 14)
                            .background(viewModel.isHandRaised ? Color.white : UsColors.bgSecondary)
                            .clipShape(Capsule())
                        }

                        HStack(spacing: 12) {
                            Button("👏") { viewModel.sendReaction("👏") }
                            Button("🔥") { viewModel.sendReaction("🔥") }
                            Button("💯") { viewModel.sendReaction("💯") }
                        }
                        .font(.system(size: 20))

                        Spacer()

                        Button("Leave Quietly") {
                            onDismiss()
                        }
                        .font(.system(size: 13, weight: .bold))
                        .foregroundColor(UsColors.liveRed)
                        .padding(.horizontal, 14)
                        .padding(.vertical, 8)
                        .background(UsColors.liveRed.opacity(0.15))
                        .clipShape(Capsule())
                    }
                    .padding(16)
                    .background(UsColors.bgSecondary)
                }
            }
            .navigationBarTitleDisplayMode(.inline)
            .toolbar {
                ToolbarItem(placement: .cancellationAction) {
                    Button("Minimize", action: onDismiss)
                        .foregroundColor(UsColors.textMuted)
                }
            }
        }
    }

    @ViewBuilder
    private func speakerCell(_ speaker: Speaker) -> some View {
        VStack(spacing: 6) {
            ZStack(alignment: .bottomTrailing) {
                // Speaking ring
                Circle()
                    .stroke(speaker.isSpeaking ? UsColors.onlineGreen : Color.clear, lineWidth: 3)
                    .frame(width: 76, height: 76)

                UsAvatar(name: speaker.name, url: speaker.avatarUrl, size: .large)

                if speaker.isMuted {
                    Image(systemName: "mic.slash.fill")
                        .font(.system(size: 12))
                        .foregroundColor(.white)
                        .padding(4)
                        .background(Color.black)
                        .clipShape(Circle())
                        .offset(x: 2, y: 2)
                }
            }

            Text(speaker.name)
                .font(.system(size: 12, weight: .bold))
                .foregroundColor(UsColors.textPrimary)
                .lineLimit(1)

            if speaker.isHost {
                Text("HOST")
                    .font(.system(size: 9, weight: .black))
                    .foregroundColor(UsColors.postbookPrimary)
                    .padding(.horizontal, 6)
                    .padding(.vertical, 2)
                    .background(UsColors.postbookPrimary.opacity(0.15))
                    .clipShape(Capsule())
            }
        }
    }
}
