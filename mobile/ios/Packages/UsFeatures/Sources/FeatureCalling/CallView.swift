import SwiftUI
import UsModel
import UsDesignSystem

public enum CallState {
    case outgoingRinging
    case incomingRinging
    case connected(durationSeconds: Int)
    case ended
}

public struct CallView: View {
    public let participant: Author
    public let isVideo: Bool
    public let onEndCall: () -> Void

    @State private var callDuration: Int = 0
    @State private var isMuted: Bool = false
    @State private var isSpeaker: Bool = false
    @State private var isVideoEnabled: Bool = true
    @State private var timerTask: Task<Void, Never>?

    public init(
        participant: Author,
        isVideo: Bool = true,
        onEndCall: @escaping () -> Void = {}
    ) {
        self.participant = participant
        self.isVideo = isVideo
        self.onEndCall = onEndCall
    }

    public var body: some View {
        ZStack {
            // Background
            if isVideo && isVideoEnabled {
                LinearGradient(
                    colors: [Color(red: 0x14/255.0, green: 0x1E/255.0, blue: 0x32/255.0),
                             Color(red: 0x0A/255.0, green: 0x0A/255.0, blue: 0x14/255.0)],
                    startPoint: .topLeading,
                    endPoint: .bottomTrailing
                )
                .ignoresSafeArea()
            } else {
                Color.black.ignoresSafeArea()
            }

            VStack(spacing: 24) {
                // Participant Info
                VStack(spacing: 12) {
                    UsAvatar(
                        name: participant.nameForDisplay,
                        url: participant.avatarUrl,
                        size: .large
                    )
                    .frame(width: 90, height: 90)

                    Text(participant.nameForDisplay)
                        .font(.system(size: 24, weight: .bold))
                        .foregroundColor(.white)

                    Text(formatDuration(callDuration))
                        .font(.system(size: 14, weight: .medium, design: .monospaced))
                        .foregroundColor(.white.opacity(0.8))
                }
                .padding(.top, 40)

                Spacer()

                // Call Controls
                HStack(spacing: 24) {
                    // Mute Button
                    callControlBtn(
                        icon: isMuted ? "mic.slash.fill" : "mic.fill",
                        active: isMuted
                    ) {
                        isMuted.toggle()
                    }

                    if isVideo {
                        // Toggle Video
                        callControlBtn(
                            icon: isVideoEnabled ? "video.fill" : "video.slash.fill",
                            active: !isVideoEnabled
                        ) {
                            isVideoEnabled.toggle()
                        }
                    }

                    // Speaker Button
                    callControlBtn(
                        icon: isSpeaker ? "speaker.wave.3.fill" : "speaker.wave.1.fill",
                        active: isSpeaker
                    ) {
                        isSpeaker.toggle()
                    }

                    // End Call (Red button)
                    Button(action: {
                        timerTask?.cancel()
                        onEndCall()
                    }) {
                        ZStack {
                            Circle()
                                .fill(UsColors.liveRed)
                                .frame(width: 64, height: 64)
                                .shadow(color: UsColors.liveRed.opacity(0.4), radius: 10)

                            Image(systemName: "phone.down.fill")
                                .font(.system(size: 24))
                                .foregroundColor(.white)
                        }
                    }
                }
                .padding(.bottom, 48)
            }
        }
        .onAppear {
            startTimer()
        }
        .onDisappear {
            timerTask?.cancel()
        }
    }

    private func callControlBtn(icon: String, active: Bool, action: @escaping () -> Void) -> some View {
        Button(action: action) {
            ZStack {
                Circle()
                    .fill(active ? Color.white : Color.white.opacity(0.2))
                    .frame(width: 56, height: 56)

                Image(systemName: icon)
                    .font(.system(size: 20))
                    .foregroundColor(active ? .black : .white)
            }
        }
        .buttonStyle(.plain)
    }

    private func startTimer() {
        timerTask = Task {
            while !Task.isCancelled {
                try? await Task.sleep(nanoseconds: 1_000_000_000)
                await MainActor.run {
                    self.callDuration += 1
                }
            }
        }
    }

    private func formatDuration(_ seconds: Int) -> String {
        let mins = seconds / 60
        let secs = seconds % 60
        return String(format: "%02d:%02d", mins, secs)
    }
}
