import SwiftUI
import UsModel
import UsDesignSystem

public struct SiriShortcutItem: Identifiable {
    public let id: String
    public let phrase: String
    public let description: String
    public let iconName: String

    public init(id: String, phrase: String, description: String, iconName: String) {
        self.id = id
        self.phrase = phrase
        self.description = description
        self.iconName = iconName
    }
}

public struct SuperAppIntentsView: View {
    public let onDismiss: () -> Void

    @State private var shortcuts: [SiriShortcutItem] = [
        SiriShortcutItem(id: "siri-1", phrase: "\"Hey Siri, send ₹500 on US App\"", description: "Instantly launch UPI transfer modal", iconName: "indianrupeesign.circle.fill"),
        SiriShortcutItem(id: "siri-2", phrase: "\"Hey Siri, scan QR on US\"", description: "Directly opens camera QR scanner viewfinder", iconName: "qrcode.viewfinder"),
        SiriShortcutItem(id: "siri-3", phrase: "\"Hey Siri, check my Gold price on US\"", description: "Read out live 24K gold investment price", iconName: "sparkles")
    ]

    public init(onDismiss: @escaping () -> Void = {}) {
        self.onDismiss = onDismiss
    }

    public var body: some View {
        NavigationStack {
            ZStack {
                UsColors.bgPrimary
                    .ignoresSafeArea()

                ScrollView {
                    VStack(alignment: .leading, spacing: 20) {
                        // Siri Hero Banner
                        HStack(spacing: 14) {
                            ZStack {
                                Circle()
                                    .fill(
                                        LinearGradient(
                                            colors: [Color.purple, Color.cyan],
                                            startPoint: .topLeading,
                                            endPoint: .bottomTrailing
                                        )
                                    )
                                    .frame(width: 48, height: 48)

                                Image(systemName: "mic.fill")
                                    .foregroundColor(.white)
                                    .font(.system(size: 20))
                            }

                            VStack(alignment: .leading, spacing: 2) {
                                Text("Siri Shortcuts & App Intents")
                                    .font(.system(size: 15, weight: .bold))
                                    .foregroundColor(UsColors.textPrimary)
                                Text("Control your Super-App with hands-free voice commands")
                                    .font(.system(size: 11))
                                    .foregroundColor(UsColors.textMuted)
                            }
                        }
                        .padding(14)
                        .frame(maxWidth: .infinity, alignment: .leading)
                        .background(UsColors.bgSecondary)
                        .clipShape(RoundedRectangle(cornerRadius: 16))

                        Text("Available Siri Voice Shortcuts")
                            .font(.system(size: 15, weight: .bold))
                            .foregroundColor(UsColors.textPrimary)

                        LazyVStack(spacing: 12) {
                            ForEach(shortcuts) { sc in
                                shortcutCard(sc)
                            }
                        }
                    }
                    .padding(16)
                }
            }
            .navigationTitle("Siri & Shortcuts")
            .navigationBarTitleDisplayMode(.inline)
            .toolbar {
                ToolbarItem(placement: .cancellationAction) {
                    Button("Close", action: onDismiss)
                        .foregroundColor(UsColors.textMuted)
                }
            }
        }
    }

    @ViewBuilder
    private func shortcutCard(_ sc: SiriShortcutItem) -> some View {
        VStack(alignment: .leading, spacing: 8) {
            HStack {
                Image(systemName: sc.iconName)
                    .font(.system(size: 18))
                    .foregroundColor(UsColors.postbookPrimary)

                Text(sc.phrase)
                    .font(.system(size: 13, weight: .bold))
                    .foregroundColor(UsColors.textPrimary)

                Spacer()

                Button(action: {
                    HapticManager.shared.trigger(.success)
                    ToastManager.shared.show("Added to Siri Shortcuts!", style: .success)
                }) {
                    Text("+ Add to Siri")
                        .font(.system(size: 11, weight: .bold))
                        .foregroundColor(.black)
                        .padding(.horizontal, 12)
                        .padding(.vertical, 6)
                        .background(Color.white)
                        .clipShape(Capsule())
                }
            }

            Text(sc.description)
                .font(.system(size: 11))
                .foregroundColor(UsColors.textMuted)
        }
        .padding(14)
        .background(UsColors.bgSecondary)
        .clipShape(RoundedRectangle(cornerRadius: 14))
    }
}
