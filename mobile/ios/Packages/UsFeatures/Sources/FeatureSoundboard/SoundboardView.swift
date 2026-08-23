import SwiftUI
import UsModel
import UsDesignSystem

public struct SoundEffect: Identifiable, Hashable {
    public let id: String
    public let name: String
    public let emoji: String
    public let color: Color

    public init(id: String, name: String, emoji: String, color: Color) {
        self.id = id
        self.name = name
        self.emoji = emoji
        self.color = color
    }
}

public struct SoundboardView: View {
    public let onTriggerSound: (SoundEffect) -> Void
    public let onDismiss: () -> Void

    private let sounds: [SoundEffect] = [
        SoundEffect(id: "s1", name: "Applause", emoji: "👏", color: .purple),
        SoundEffect(id: "s2", name: "Airhorn", emoji: "📢", color: .red),
        SoundEffect(id: "s3", name: "Rimshot", emoji: "🥁", color: .orange),
        SoundEffect(id: "s4", name: "Laugh Track", emoji: "😂", color: .yellow),
        SoundEffect(id: "s5", name: "Wow & Cheer", emoji: "🌟", color: .pink),
        SoundEffect(id: "s6", name: "Coin Drop", emoji: "🪙", color: .cyan)
    ]

    private let columns = [
        GridItem(.flexible(), spacing: 14),
        GridItem(.flexible(), spacing: 14),
        GridItem(.flexible(), spacing: 14)
    ]

    public init(
        onTriggerSound: @escaping (SoundEffect) -> Void = { _ in },
        onDismiss: @escaping () -> Void = {}
    ) {
        self.onTriggerSound = onTriggerSound
        self.onDismiss = onDismiss
    }

    public var body: some View {
        NavigationStack {
            ZStack {
                UsColors.bgPrimary
                    .ignoresSafeArea()

                VStack(spacing: 20) {
                    Text("Broadcast Soundboard")
                        .font(.system(size: 13, weight: .semibold))
                        .foregroundColor(UsColors.textMuted)

                    LazyVGrid(columns: columns, spacing: 14) {
                        ForEach(sounds) { sound in
                            Button(action: {
                                HapticManager.shared.trigger(.medium)
                                onTriggerSound(sound)
                                ToastManager.shared.show("Played \(sound.emoji) \(sound.name)", style: .info)
                            }) {
                                VStack(spacing: 8) {
                                    Text(sound.emoji)
                                        .font(.system(size: 38))

                                    Text(sound.name)
                                        .font(.system(size: 12, weight: .bold))
                                        .foregroundColor(UsColors.textPrimary)
                                        .lineLimit(1)
                                }
                                .frame(maxWidth: .infinity)
                                .frame(height: 100)
                                .background(UsColors.bgSecondary)
                                .clipShape(RoundedRectangle(cornerRadius: 16))
                                .overlay(
                                    RoundedRectangle(cornerRadius: 16)
                                        .stroke(sound.color.opacity(0.4), lineWidth: 1.5)
                                )
                            }
                            .buttonStyle(.plain)
                        }
                    }

                    Spacer()
                }
                .padding(16)
            }
            .navigationTitle("Live Soundboard")
            .navigationBarTitleDisplayMode(.inline)
            .toolbar {
                ToolbarItem(placement: .cancellationAction) {
                    Button("Done", action: onDismiss)
                        .foregroundColor(UsColors.textMuted)
                }
            }
        }
    }
}
