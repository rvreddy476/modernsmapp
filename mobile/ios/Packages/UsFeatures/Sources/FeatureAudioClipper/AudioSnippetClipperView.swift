import SwiftUI
import UsModel
import UsDesignSystem

public struct AudioSnippetClipperView: View {
    public let sourceTitle: String
    public let speakerName: String
    public let onDismiss: () -> Void

    @State private var trimStart: Double = 5.0
    @State private var trimDuration: Double = 15.0
    @State private var isPlayingClip: Bool = false

    public init(
        sourceTitle: String = "Building India's #1 Social Super-App",
        speakerName: String = "Sarah Chen",
        onDismiss: @escaping () -> Void = {}
    ) {
        self.sourceTitle = sourceTitle
        self.speakerName = speakerName
        self.onDismiss = onDismiss
    }

    public var body: some View {
        NavigationStack {
            ZStack {
                UsColors.bgPrimary
                    .ignoresSafeArea()

                VStack(spacing: 24) {
                    // Header info
                    VStack(spacing: 4) {
                        Text(sourceTitle)
                            .font(.system(size: 16, weight: .bold))
                            .foregroundColor(UsColors.textPrimary)
                            .multilineTextAlignment(.center)

                        Text("Spoken by \(speakerName)")
                            .font(.system(size: 12))
                            .foregroundColor(UsColors.textMuted)
                    }
                    .padding(.top, 12)

                    // Waveform Timeline Scrubber
                    VStack(alignment: .leading, spacing: 10) {
                        HStack {
                            Text("15s Viral Soundbite Clipper ✂️")
                                .font(.system(size: 13, weight: .bold))
                                .foregroundColor(UsColors.postbookPrimary)

                            Spacer()

                            Text("0:\(Int(trimStart)) - 0:\(Int(trimStart + trimDuration))")
                                .font(.system(size: 12, weight: .bold, design: .monospaced))
                                .foregroundColor(UsColors.onlineGreen)
                        }

                        // Simulated Waveform Bars
                        HStack(alignment: .center, spacing: 3) {
                            ForEach(0..<28, id: \.self) { idx in
                                let isSelected = Double(idx) >= trimStart * 1.4 && Double(idx) <= (trimStart + trimDuration) * 1.4
                                Capsule()
                                    .fill(isSelected ? UsColors.postbookPrimary : Color.white.opacity(0.2))
                                    .frame(width: 4, height: CGFloat([12, 28, 40, 22, 34, 18, 48, 30, 16, 38, 24, 44, 32, 20][idx % 14]))
                            }
                        }
                        .frame(height: 60)
                        .padding(.vertical, 10)

                        // Slider control
                        Slider(value: $trimStart, in: 0...20, step: 1.0)
                            .tint(UsColors.postbookPrimary)
                    }
                    .padding(16)
                    .background(UsColors.bgSecondary)
                    .clipShape(RoundedRectangle(cornerRadius: 18))

                    // Play Clip preview button
                    Button(action: {
                        isPlayingClip.toggle()
                        HapticManager.shared.trigger(.selection)
                    }) {
                        HStack(spacing: 8) {
                            Image(systemName: isPlayingClip ? "pause.fill" : "play.fill")
                            Text(isPlayingClip ? "Pause Audio Clip" : "Preview 15s Snippet")
                        }
                        .font(.system(size: 14, weight: .bold))
                        .foregroundColor(.white)
                        .padding(.horizontal, 20)
                        .padding(.vertical, 12)
                        .background(UsColors.bgSecondary)
                        .clipShape(Capsule())
                        .overlay(Capsule().stroke(UsColors.borderSubtle, lineWidth: 1))
                    }

                    Spacer()

                    // Export / Share Actions
                    HStack(spacing: 12) {
                        Button(action: {
                            HapticManager.shared.trigger(.success)
                            ToastManager.shared.show("Clip added to Story draft! 🚀", style: .success)
                            onDismiss()
                        }) {
                            HStack {
                                Spacer()
                                Image(systemName: "plus.circle.fill")
                                Text("Share to Story")
                                    .font(.system(size: 14, weight: .bold))
                                Spacer()
                            }
                            .padding(.vertical, 14)
                            .foregroundColor(.black)
                            .background(Color.white)
                            .clipShape(RoundedRectangle(cornerRadius: 14))
                        }

                        Button(action: {
                            HapticManager.shared.trigger(.success)
                            ToastManager.shared.show("Sent clip to DM inbox!", style: .success)
                            onDismiss()
                        }) {
                            Image(systemName: "paperplane.fill")
                                .font(.system(size: 16))
                                .foregroundColor(.white)
                                .padding(14)
                                .background(UsColors.postbookPrimary)
                                .clipShape(RoundedRectangle(cornerRadius: 14))
                        }
                    }
                    .padding(.horizontal, 16)
                }
                .padding(16)
            }
            .navigationTitle("Audio Clipper")
            .navigationBarTitleDisplayMode(.inline)
            .toolbar {
                ToolbarItem(placement: .cancellationAction) {
                    Button("Close", action: onDismiss)
                        .foregroundColor(UsColors.textMuted)
                }
            }
        }
    }
}
