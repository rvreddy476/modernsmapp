import SwiftUI
import UsModel
import UsDesignSystem

// P0-6 (chat production correction pass): this screen was a mocked "Secret
// Chat" that displayed "encrypted on-device with zero server logs" and a
// "Type encrypted message" composer over a plaintext backend. No end-to-end
// encryption exists in this product yet (see docs/adr/
// adr-chat-e2ee-implementation.md), and the directive forbids every E2EE
// claim until CH-LB-5 passes. The public API is preserved; the surface now
// states the truth and offers nothing that implies cryptography.

public struct SecretChatMessage: Identifiable {
    public let id: String
    public let text: String
    public let isSender: Bool
    public var remainingSeconds: Int

    public init(id: String, text: String, isSender: Bool, remainingSeconds: Int = 10) {
        self.id = id
        self.text = text
        self.isSender = isSender
        self.remainingSeconds = remainingSeconds
    }
}

public struct SecretChatView: View {
    public let recipientName: String
    public let onDismiss: () -> Void

    public init(
        recipientName: String = "",
        onDismiss: @escaping () -> Void = {}
    ) {
        self.recipientName = recipientName
        self.onDismiss = onDismiss
    }

    public var body: some View {
        NavigationStack {
            ZStack {
                UsColors.bgPrimary
                    .ignoresSafeArea()

                VStack(spacing: 16) {
                    Image(systemName: "hourglass")
                        .font(.system(size: 40))
                        .foregroundColor(UsColors.textMuted)

                    Text("Secret chats aren't available yet")
                        .font(.system(size: 18, weight: .bold))
                        .foregroundColor(UsColors.textPrimary)

                    Text("Messages in this app are protected in transit and at rest, but they are not end-to-end encrypted today. We won't offer a secret chat until it truly is one.")
                        .font(.system(size: 13))
                        .foregroundColor(UsColors.textMuted)
                        .multilineTextAlignment(.center)
                        .padding(.horizontal, 24)
                }
            }
            .navigationTitle("Secret Chat")
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
