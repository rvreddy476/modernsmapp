import SwiftUI
import AVFoundation

@Observable
public final class VoiceNotePlayerManager: NSObject, AVAudioPlayerDelegate, @unchecked Sendable {
    public var isPlaying: Bool = false
    public var currentTime: TimeInterval = 0
    public var duration: TimeInterval = 0
    public var playbackRate: Float = 1.0

    private var audioPlayer: AVAudioPlayer?
    private var progressTimer: Timer?

    public func load(url: URL) {
        do {
            audioPlayer = try AVAudioPlayer(contentsOf: url)
            audioPlayer?.delegate = self
            audioPlayer?.enableRate = true
            duration = audioPlayer?.duration ?? 0
        } catch {
            duration = 0
        }
    }

    public func togglePlay() {
        guard let player = audioPlayer else { return }
        if isPlaying {
            player.pause()
            progressTimer?.invalidate()
            isPlaying = false
        } else {
            player.rate = playbackRate
            player.play()
            isPlaying = true
            progressTimer = Timer.scheduledTimer(withTimeInterval: 0.05, repeats: true) { [weak self] _ in
                guard let self = self, let p = self.audioPlayer else { return }
                self.currentTime = p.currentTime
                if !p.isPlaying {
                    self.isPlaying = false
                    self.currentTime = 0
                    self.progressTimer?.invalidate()
                }
            }
        }
    }

    public func setSpeed(_ speed: Float) {
        playbackRate = speed
        if isPlaying {
            audioPlayer?.rate = speed
        }
    }

    public func audioPlayerDidFinishPlaying(_ player: AVAudioPlayer, successfully flag: Bool) {
        isPlaying = false
        currentTime = 0
        progressTimer?.invalidate()
    }
}

public struct VoiceNotePlayerView: View {
    public let audioURL: URL?
    public let simulatedDuration: TimeInterval
    @State private var playerManager = VoiceNotePlayerManager()

    public init(audioURL: URL? = nil, simulatedDuration: TimeInterval = 14.0) {
        self.audioURL = audioURL
        self.simulatedDuration = simulatedDuration
    }

    public var body: some View {
        HStack(spacing: 12) {
            // Play / Pause Button
            Button(action: {
                playerManager.togglePlay()
            }) {
                ZStack {
                    Circle()
                        .fill(Color.white)
                        .frame(width: 36, height: 36)

                    Image(systemName: playerManager.isPlaying ? "pause.fill" : "play.fill")
                        .font(.system(size: 14, weight: .bold))
                        .foregroundColor(.black)
                        .offset(x: playerManager.isPlaying ? 0 : 1)
                }
            }
            .buttonStyle(.plain)

            // Waveform scrubber bar
            VStack(alignment: .leading, spacing: 4) {
                HStack(spacing: 3) {
                    ForEach(0..<24, id: \.self) { idx in
                        let height: CGFloat = [12, 18, 26, 14, 22, 30, 16, 28, 20, 14, 24, 18, 12, 28, 16, 22, 14, 26, 30, 18, 12, 20, 14, 10][idx]
                        let progress = playerManager.duration > 0 ? playerManager.currentTime / playerManager.duration : 0
                        let isPlayed = Double(idx) / 24.0 <= progress

                        Capsule()
                            .fill(isPlayed ? Color.white : Color.white.opacity(0.3))
                            .frame(width: 3, height: height)
                    }
                }
                .frame(height: 32)

                HStack {
                    Text(formatTime(playerManager.isPlaying ? playerManager.currentTime : (playerManager.duration > 0 ? playerManager.duration : simulatedDuration)))
                        .font(.system(size: 11, weight: .medium, design: .monospaced))
                        .foregroundColor(.white.opacity(0.7))

                    Spacer()
                }
            }

            // Speed Toggle Button (1x, 1.5x, 2x)
            Button(action: cycleSpeed) {
                Text(String(format: "%.1fx", playerManager.playbackRate).replacingOccurrences(of: ".0x", with: "x"))
                    .font(.system(size: 11, weight: .bold))
                    .foregroundColor(.white)
                    .padding(.horizontal, 8)
                    .padding(.vertical, 4)
                    .background(Color.white.opacity(0.2))
                    .clipShape(Capsule())
            }
            .buttonStyle(.plain)
        }
        .padding(12)
        .background(Color(red: 0x1E/255.0, green: 0x1E/255.0, blue: 0x2A/255.0))
        .clipShape(RoundedRectangle(cornerRadius: 16))
        .onAppear {
            if let url = audioURL {
                playerManager.load(url: url)
            }
        }
    }

    private func cycleSpeed() {
        if playerManager.playbackRate == 1.0 {
            playerManager.setSpeed(1.5)
        } else if playerManager.playbackRate == 1.5 {
            playerManager.setSpeed(2.0)
        } else {
            playerManager.setSpeed(1.0)
        }
    }

    private func formatTime(_ time: TimeInterval) -> String {
        let mins = Int(time) / 60
        let secs = Int(time) % 60
        return String(format: "%02d:%02d", mins, secs)
    }
}
