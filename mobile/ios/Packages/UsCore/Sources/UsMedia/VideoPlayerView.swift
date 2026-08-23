import SwiftUI
import AVKit
import UsModel
import UsDesignSystem

public struct VideoPlayerView: View {
    public let videoURL: URL
    public let posterURL: URL?
    public let isPlaying: Bool
    public let isMuted: Bool

    @State private var player: AVQueuePlayer?
    @State private var looper: AVPlayerLooper?
    @State private var isReadyToPlay: Bool = false

    public init(
        videoURL: URL,
        posterURL: URL? = nil,
        isPlaying: Bool = true,
        isMuted: Bool = true
    ) {
        self.videoURL = videoURL
        self.posterURL = posterURL
        self.isPlaying = isPlaying
        self.isMuted = isMuted
    }

    public var body: some View {
        ZStack {
            if let player = player {
                CustomAVPlayerRepresentable(player: player)
                    .ignoresSafeArea()
                    .opacity(isReadyToPlay ? 1.0 : 0.0)
                    .animation(.easeInOut(duration: 0.25), value: isReadyToPlay)
            }

            if !isReadyToPlay, let poster = posterURL {
                AsyncImage(url: poster) { phase in
                    switch phase {
                    case .success(let image):
                        image
                            .resizable()
                            .scaledToFill()
                    default:
                        Rectangle()
                            .fill(UsColors.bgTertiary)
                    }
                }
                .ignoresSafeArea()
            }
        }
        .onAppear {
            setupPlayer()
        }
        .onDisappear {
            teardownPlayer()
        }
        .onChange(of: isPlaying) { _, newValue in
            if newValue {
                player?.play()
            } else {
                player?.pause()
            }
        }
        .onChange(of: isMuted) { _, newValue in
            player?.isMuted = newValue
        }
    }

    private func setupPlayer() {
        let item = AVPlayerItem(url: videoURL)
        let queuePlayer = AVQueuePlayer(playerItem: item)
        queuePlayer.isMuted = isMuted
        queuePlayer.actionAtItemEnd = .none

        self.looper = AVPlayerLooper(player: queuePlayer, templateItem: item)
        self.player = queuePlayer

        if isPlaying {
            queuePlayer.play()
        }

        DispatchQueue.main.asyncAfter(deadline: .now() + 0.15) {
            self.isReadyToPlay = true
        }
    }

    private func teardownPlayer() {
        player?.pause()
        player = nil
        looper = nil
        isReadyToPlay = false
    }
}

private struct CustomAVPlayerRepresentable: UIViewControllerRepresentable {
    let player: AVPlayer

    func makeUIViewController(context: Context) -> AVPlayerViewController {
        let controller = AVPlayerViewController()
        controller.player = player
        controller.showsPlaybackControls = false
        controller.videoGravity = .resizeAspectFill
        controller.view.backgroundColor = .black
        return controller
    }

    func updateUIViewController(_ uiViewController: AVPlayerViewController, context: Context) {
        uiViewController.player = player
    }
}
