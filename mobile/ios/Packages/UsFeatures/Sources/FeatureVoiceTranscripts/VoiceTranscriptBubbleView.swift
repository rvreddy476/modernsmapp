import SwiftUI
import UsModel
import UsDesignSystem

public struct VoiceTranscriptBubbleView: View {
    public let audioDuration: String
    public let transcriptText: String
    public let detectedLanguage: String

    @State private var isPlaying: Bool = false
    @State private var isExpanded: Bool = true

    public init(
        audioDuration: String = "0:34",
        transcriptText: String = "Hey Alex! Let's wrap up the Swift 5.9 super-app release today and run the final SPM test suite before midnight. 🚀",
        detectedLanguage: String = "English (India)"
    ) {
        self.audioDuration = audioDuration
        self.transcriptText = transcriptText
        self.detectedLanguage = detectedLanguage
    }

    public var body: some View {
        VStack(alignment: .leading, spacing: 10) {
            // Audio Player Bar
            HStack(spacing: 10) {
                Button(action: {
                    isPlaying.toggle()
                    HapticManager.shared.trigger(.selection)
                }) {
                    Image(systemName: isPlaying ? "pause.fill" : "play.fill")
                        .font(.system(size: 14))
                        .foregroundColor(.black)
                        .padding(8)
                        .background(Color.white)
                        .clipShape(Circle())
                }

                // Waveform bars simulation
                HStack(spacing: 3) {
                    ForEach([8, 16, 24, 12, 28, 18, 10, 22, 14, 20, 8, 16, 24, 12], id: \.self) { height in
                        Capsule()
                            .fill(isPlaying ? UsColors.postbookPrimary : Color.white.opacity(0.4))
                            .frame(width: 3, height: CGFloat(height))
                    }
                }

                Spacer()

                Text(audioDuration)
                    .font(.system(size: 11, weight: .monospaced))
                    .foregroundColor(UsColors.textMuted)
            }

            Divider().background(UsColors.borderSubtle)

            // Transcript text
            VStack(alignment: .leading, spacing: 4) {
                HStack {
                    Text("Auto-Transcript • \(detectedLanguage)")
                        .font(.system(size: 10, weight: .bold))
                        .foregroundColor(UsColors.postbookPrimary)

                    Spacer()

                    Button(action: {
                        HapticManager.shared.trigger(.selection)
                        ToastManager.shared.show("Transcript copied to clipboard!", style: .info)
                    }) {
                        Image(systemName: "doc.on.doc")
                            .font(.system(size: 10))
                            .foregroundColor(UsColors.textMuted)
                    }
                }

                Text(transcriptText)
                    .font(.system(size: 12))
                    .foregroundColor(UsColors.textPrimary)
                    .lineLimit(isExpanded ? nil : 2)
            }
        }
        .padding(14)
        .background(UsColors.bgSecondary)
        .clipShape(RoundedRectangle(cornerRadius: 16))
        .frame(width: 290)
    }
}
