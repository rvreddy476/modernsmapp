import SwiftUI
import AVKit
import UsModel
import UsDesignSystem

public struct WatchVideoPlayerView: View {
    public let videoURL: URL
    public let posterURL: URL?

    @State private var player: AVPlayer?
    @State private var isPlaying: Bool = true
    @State private var showControls: Bool = true
    @State private var currentTime: Double = 0
    @State private var duration: Double = 0
    @State private var playbackRate: Float = 1.0
    @State private var isDraggingScrubber: Bool = false
    @State private var hideControlsTask: Task<Void, Never>?

    public init(videoURL: URL, posterURL: URL? = nil) {
        self.videoURL = videoURL
        self.posterURL = posterURL
    }

    public var body: some View {
        ZStack {
            Color.black

            // Video layer
            if let player = player {
                CustomVideoPlayerRepresentable(player: player)
                    .onTapGesture {
                        withAnimation {
                            showControls.toggle()
                        }
                        if showControls {
                            scheduleControlsHide()
                        }
                    }
            }

            // Controls Overlay
            if showControls {
                controlsOverlay
                    .transition(.opacity)
            }
        }
        .aspectRatio(16 / 9, contentMode: .fit)
        .onAppear {
            setupPlayer()
        }
        .onDisappear {
            teardownPlayer()
        }
    }

    private var controlsOverlay: some View {
        ZStack {
            Color.black.opacity(0.4)

            // Center: Rewind 10s, Play/Pause, Forward 10s
            HStack(spacing: 40) {
                Button(action: { seek(by: -10) }) {
                    Image(systemName: "gobackward.10")
                        .font(.system(size: 28))
                        .foregroundColor(.white)
                }

                Button(action: togglePlayPause) {
                    Image(systemName: isPlaying ? "pause.fill" : "play.fill")
                        .font(.system(size: 38))
                        .foregroundColor(.white)
                }

                Button(action: { seek(by: 10) }) {
                    Image(systemName: "goforward.10")
                        .font(.system(size: 28))
                        .foregroundColor(.white)
                }
            }

            // Bottom bar: Time elapsed, Scrubber slider, Duration, Speed selector
            VStack {
                Spacer()
                VStack(spacing: 4) {
                    HStack(spacing: 8) {
                        Text(formatTime(currentTime))
                            .font(.system(size: 12, weight: .medium, design: .monospaced))
                            .foregroundColor(.white)

                        Slider(
                            value: Binding(
                                get: { currentTime },
                                set: { newValue in
                                    currentTime = newValue
                                    seekTo(newValue)
                                }
                            ),
                            in: 0...max(duration, 1)
                        )
                        .tint(UsColors.postbookPrimary)

                        Text(formatTime(duration))
                            .font(.system(size: 12, weight: .medium, design: .monospaced))
                            .foregroundColor(.white.opacity(0.8))

                        Menu {
                            Button("0.5x") { changeRate(0.5) }
                            Button("1.0x (Normal)") { changeRate(1.0) }
                            Button("1.25x") { changeRate(1.25) }
                            Button("1.5x") { changeRate(1.5) }
                            Button("2.0x") { changeRate(2.0) }
                        } label: {
                            Text(String(format: "%.2gx", playbackRate))
                                .font(.system(size: 12, weight: .bold))
                                .foregroundColor(.white)
                                .padding(.horizontal, 6)
                                .padding(.vertical, 3)
                                .background(Color.white.opacity(0.2))
                                .clipShape(RoundedRectangle(cornerRadius: 4))
                        }
                    }
                    .padding(.horizontal, 12)
                }
                .padding(.bottom, 8)
            }
        }
    }

    private func setupPlayer() {
        let player = AVPlayer(url: videoURL)
        self.player = player

        let interval = CMTime(seconds: 0.5, preferredTimescale: CMTimeScale(NSEC_PER_SEC))
        player.addPeriodicTimeObserver(forInterval: interval, queue: .main) { time in
            if !self.isDraggingScrubber {
                self.currentTime = time.seconds
            }
            if let item = player.currentItem, item.duration.isValid {
                self.duration = item.duration.seconds
            }
        }

        player.play()
        isPlaying = true
        scheduleControlsHide()
    }

    private func teardownPlayer() {
        player?.pause()
        player = nil
        hideControlsTask?.cancel()
    }

    private func togglePlayPause() {
        guard let player = player else { return }
        if isPlaying {
            player.pause()
            isPlaying = false
            hideControlsTask?.cancel()
        } else {
            player.play()
            isPlaying = true
            scheduleControlsHide()
        }
    }

    private func seek(by delta: Double) {
        guard let player = player else { return }
        let target = max(0, min(currentTime + delta, duration))
        seekTo(target)
    }

    private func seekTo(_ seconds: Double) {
        guard let player = player else { return }
        let time = CMTime(seconds: seconds, preferredTimescale: 600)
        player.seek(to: time, toleranceBefore: .zero, toleranceAfter: .zero)
        scheduleControlsHide()
    }

    private func changeRate(_ rate: Float) {
        playbackRate = rate
        player?.rate = rate
        isPlaying = true
        scheduleControlsHide()
    }

    private func scheduleControlsHide() {
        hideControlsTask?.cancel()
        hideControlsTask = Task {
            try? await Task.sleep(nanoseconds: 3_500_000_000)
            guard !Task.isCancelled else { return }
            await MainActor.run {
                withAnimation {
                    self.showControls = false
                }
            }
        }
    }

    private func formatTime(_ seconds: Double) -> String {
        guard !seconds.isNaN && !seconds.isInfinite else { return "0:00" }
        let mins = Int(seconds) / 60
        let secs = Int(seconds) % 60
        return String(format: "%d:%02d", mins, secs)
    }
}

private struct CustomVideoPlayerRepresentable: UIViewControllerRepresentable {
    let player: AVPlayer

    func makeUIViewController(context: Context) -> AVPlayerViewController {
        let vc = AVPlayerViewController()
        vc.player = player
        vc.showsPlaybackControls = false
        vc.videoGravity = .resizeAspect
        vc.view.backgroundColor = .black
        return vc
    }

    func updateUIViewController(_ uiViewController: AVPlayerViewController, context: Context) {
        uiViewController.player = player
    }
}
