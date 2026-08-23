import SwiftUI
import UsModel
import UsDesignSystem

public enum ViewOnceState {
    case unread
    case viewing(remainingSeconds: Int)
    case expired
}

public struct ViewOnceMediaView: View {
    public let imageUrl: String
    public let senderName: String

    @State private var state: ViewOnceState = .unread
    @State private var showFullScreen: Bool = false
    @State private var countdown: Int = 5

    public init(
        imageUrl: String = "https://images.unsplash.com/photo-1517841905240-472988babdf9?w=800",
        senderName: String = "Sarah"
    ) {
        self.imageUrl = imageUrl
        self.senderName = senderName
    }

    public var body: some View {
        Group {
            switch state {
            case .unread:
                Button(action: openMedia) {
                    HStack(spacing: 8) {
                        Image(systemName: "1.circle.fill")
                            .font(.system(size: 18))
                            .foregroundColor(UsColors.postgramPrimary)

                        Text("View-Once Photo")
                            .font(.system(size: 14, weight: .semibold))
                            .foregroundColor(UsColors.textPrimary)
                    }
                    .padding(.horizontal, 14)
                    .padding(.vertical, 10)
                    .background(UsColors.bgSecondary)
                    .clipShape(Capsule())
                    .overlay(Capsule().stroke(UsColors.postgramPrimary.opacity(0.4), lineWidth: 1))
                }
                .buttonStyle(.plain)

            case .viewing:
                Text("Viewing...")
                    .font(.system(size: 12))
                    .foregroundColor(UsColors.textMuted)

            case .expired:
                HStack(spacing: 6) {
                    Image(systemName: "clock.arrow.circlepath")
                        .font(.system(size: 14))
                    Text("Opened")
                        .font(.system(size: 13, weight: .medium))
                }
                .foregroundColor(UsColors.textDim)
                .padding(.horizontal, 12)
                .padding(.vertical, 6)
                .background(UsColors.bgTertiary)
                .clipShape(Capsule())
            }
        }
        .fullScreenCover(isPresented: $showFullScreen) {
            fullScreenViewOnceExperience
        }
    }

    private func openMedia() {
        HapticManager.shared.trigger(.selection)
        showFullScreen = true
        state = .viewing(remainingSeconds: 5)
        countdown = 5

        Timer.scheduledTimer(withTimeInterval: 1.0, repeats: true) { timer in
            countdown -= 1
            if countdown <= 0 {
                timer.invalidate()
                showFullScreen = false
                state = .expired
                HapticManager.shared.trigger(.medium)
            }
        }
    }

    private var fullScreenViewOnceExperience: some View {
        ZStack {
            Color.black.ignoresSafeArea()

            VStack {
                // Top Countdown Bar
                HStack {
                    Text("Photo from \(senderName)")
                        .font(.system(size: 14, weight: .bold))
                        .foregroundColor(.white)

                    Spacer()

                    HStack(spacing: 4) {
                        Image(systemName: "timer")
                        Text("\(countdown)s")
                            .font(.system(size: 14, weight: .bold, design: .monospaced))
                    }
                    .foregroundColor(.white)
                    .padding(.horizontal, 10)
                    .padding(.vertical, 4)
                    .background(Color.white.opacity(0.2))
                    .clipShape(Capsule())
                }
                .padding(.horizontal, 16)
                .padding(.top, 16)

                Spacer()

                // Media Image
                if let url = URL(string: imageUrl) {
                    AsyncImage(url: url) { phase in
                        switch phase {
                        case .success(let img):
                            img.resizable().scaledToFit()
                        default:
                            Rectangle().fill(UsColors.bgSecondary)
                        }
                    }
                    .frame(maxWidth: .infinity, maxHeight: .infinity)
                    .clipShape(RoundedRectangle(cornerRadius: 16))
                    .padding(16)
                }

                Spacer()

                Text("This photo will self-destruct after viewing")
                    .font(.system(size: 12))
                    .foregroundColor(.white.opacity(0.6))
                    .padding(.bottom, 24)
            }
        }
    }
}
