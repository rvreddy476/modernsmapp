import SwiftUI
import UsModel
import UsDesignSystem

public struct CarPlayAudioView: View {
    public let onDismiss: () -> Void

    @State private var isPlaying: Bool = true
    @State private var currentChannel: String = "Bangalore Tech Huddle (Live 🟢)"
    @State private var activeSpeakersCount: Int = 14

    public init(onDismiss: @escaping () -> Void = {}) {
        self.onDismiss = onDismiss
    }

    public var body: some View {
        NavigationStack {
            ZStack {
                Color.black
                    .ignoresSafeArea()

                VStack(spacing: 24) {
                    // CarPlay Header Badge
                    HStack(spacing: 8) {
                        Image(systemName: "car.fill")
                            .foregroundColor(Color.cyan)
                        Text("Apple CarPlay Driving Mode")
                            .font(.system(size: 13, weight: .bold))
                            .foregroundColor(Color.cyan)
                    }
                    .padding(.top, 16)

                    // Oversized Now Playing Display
                    VStack(spacing: 8) {
                        Text("NOW LISTENING")
                            .font(.system(size: 12, weight: .black))
                            .foregroundColor(UsColors.postbookPrimary)

                        Text(currentChannel)
                            .font(.system(size: 22, weight: .bold))
                            .foregroundColor(.white)
                            .multilineTextAlignment(.center)
                            .padding(.horizontal, 20)

                        Text("\(activeSpeakersCount) Speaking • 420 Listening")
                            .font(.system(size: 14))
                            .foregroundColor(UsColors.onlineGreen)
                    }
                    .padding(.vertical, 20)

                    // Oversized Driving-Safe Transport Controls
                    HStack(spacing: 40) {
                        Button(action: {
                            HapticManager.shared.trigger(.selection)
                            ToastManager.shared.show("Previous Channel", style: .info)
                        }) {
                            Image(systemName: "backward.fill")
                                .font(.system(size: 28))
                                .foregroundColor(.white)
                                .frame(width: 64, height: 64)
                                .background(Color.white.opacity(0.15))
                                .clipShape(Circle())
                        }

                        Button(action: {
                            isPlaying.toggle()
                            HapticManager.shared.trigger(.selection)
                        }) {
                            Image(systemName: isPlaying ? "pause.fill" : "play.fill")
                                .font(.system(size: 36))
                                .foregroundColor(.black)
                                .frame(width: 80, height: 80)
                                .background(Color.white)
                                .clipShape(Circle())
                        }

                        Button(action: {
                            HapticManager.shared.trigger(.selection)
                            ToastManager.shared.show("Next Channel", style: .info)
                        }) {
                            Image(systemName: "forward.fill")
                                .font(.system(size: 28))
                                .foregroundColor(.white)
                                .frame(width: 64, height: 64)
                                .background(Color.white.opacity(0.15))
                                .clipShape(Circle())
                        }
                    }

                    Spacer()

                    Text("Hands-free Siri control is enabled")
                        .font(.system(size: 12))
                        .foregroundColor(Color.gray)
                        .padding(.bottom, 20)
                }
            }
            .navigationTitle("CarPlay Audio")
            .navigationBarTitleDisplayMode(.inline)
            .toolbar {
                ToolbarItem(placement: .cancellationAction) {
                    Button("Close", action: onDismiss)
                        .foregroundColor(Color.gray)
                }
            }
        }
    }
}
