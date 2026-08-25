import SwiftUI
import UsModel
import UsDesignSystem

public enum VoiceEffectPreset: String, CaseIterable, Identifiable {
    case original = "Original 🎙️"
    case chipmunk = "Chipmunk 🐿️"
    case robot = "Robot 🤖"
    case deep = "Deep Voice 🦁"
    case helium = "Helium 🎈"
    case echo = "Echo Cave 🏔️"

    public var id: String { rawValue }
}

public struct VoiceFXView: View {
    public let onSelectEffect: (VoiceEffectPreset) -> Void
    public let onDismiss: () -> Void

    @State private var selectedPreset: VoiceEffectPreset = .original

    public init(
        onSelectEffect: @escaping (VoiceEffectPreset) -> Void = { _ in },
        onDismiss: @escaping () -> Void = {}
    ) {
        self.onSelectEffect = onSelectEffect
        self.onDismiss = onDismiss
    }

    public var body: some View {
        NavigationStack {
            ZStack {
                UsColors.bgPrimary
                    .ignoresSafeArea()

                VStack(spacing: 20) {
                    Text("Transform Your Voice Recording")
                        .font(.system(size: 13))
                        .foregroundColor(UsColors.textMuted)

                    LazyVGrid(columns: [GridItem(.flexible()), GridItem(.flexible())], spacing: 14) {
                        ForEach(VoiceEffectPreset.allCases) { preset in
                            presetCard(preset)
                        }
                    }

                    Spacer()

                    Button(action: {
                        HapticManager.shared.trigger(.success)
                        ToastManager.shared.show("Applied \(selectedPreset.rawValue) Voice FX!", style: .success)
                        onSelectEffect(selectedPreset)
                        onDismiss()
                    }) {
                        HStack {
                            Spacer()
                            Text("Apply Effect")
                                .font(.system(size: 15, weight: .bold))
                                .foregroundColor(.black)
                            Spacer()
                        }
                        .padding(.vertical, 16)
                        .background(Color.white)
                        .clipShape(RoundedRectangle(cornerRadius: 14))
                    }
                }
                .padding(16)
            }
            .navigationTitle("Voice Effects")
            .navigationBarTitleDisplayMode(.inline)
            .toolbar {
                ToolbarItem(placement: .cancellationAction) {
                    Button("Cancel", action: onDismiss)
                        .foregroundColor(UsColors.textMuted)
                }
            }
        }
    }

    @ViewBuilder
    private func presetCard(_ preset: VoiceEffectPreset) -> some View {
        let isSelected = selectedPreset == preset
        Button(action: {
            selectedPreset = preset
            HapticManager.shared.trigger(.selection)
        }) {
            HStack {
                Text(preset.rawValue)
                    .font(.system(size: 14, weight: .semibold))
                    .foregroundColor(UsColors.textPrimary)

                Spacer()

                if isSelected {
                    Image(systemName: "checkmark.circle.fill")
                        .foregroundColor(UsColors.postbookPrimary)
                }
            }
            .padding(16)
            .background(UsColors.bgSecondary)
            .clipShape(RoundedRectangle(cornerRadius: 14))
            .overlay(
                RoundedRectangle(cornerRadius: 14)
                    .stroke(isSelected ? UsColors.postbookPrimary : UsColors.borderSubtle, lineWidth: isSelected ? 1.5 : 1)
            )
        }
        .buttonStyle(.plain)
    }
}
