import SwiftUI
import UsModel
import UsDesignSystem

public struct ConfessionStickerView: View {
    public let prompt: String
    public let onSubmitted: (String) -> Void

    @State private var secretText: String = ""
    @State private var hasSubmitted: Bool = false

    public init(
        prompt: String = "Send me an anonymous secret / confession 🤫",
        onSubmitted: @escaping (String) -> Void = { _ in }
    ) {
        self.prompt = prompt
        self.onSubmitted = onSubmitted
    }

    public var body: some View {
        VStack(spacing: 12) {
            // Header
            HStack(spacing: 8) {
                Text("🤫")
                    .font(.system(size: 20))

                Text(prompt)
                    .font(.system(size: 13, weight: .bold))
                    .foregroundColor(.white)
                    .lineLimit(2)
            }

            if !hasSubmitted {
                TextField("Type your anonymous confession...", text: $secretText)
                    .font(.system(size: 12))
                    .padding(10)
                    .background(Color.white.opacity(0.15))
                    .clipShape(RoundedRectangle(cornerRadius: 10))
                    .foregroundColor(.white)

                Button(action: {
                    guard !secretText.isEmpty else { return }
                    HapticManager.shared.trigger(.success)
                    ToastManager.shared.show("Anonymous secret sent!", style: .success)
                    onSubmitted(secretText)
                    hasSubmitted = true
                }) {
                    HStack {
                        Spacer()
                        Text("Send Anonymously 🔒")
                            .font(.system(size: 12, weight: .bold))
                            .foregroundColor(.black)
                        Spacer()
                    }
                    .padding(.vertical, 8)
                    .background(Color.white)
                    .clipShape(Capsule())
                }
            } else {
                Text("Secret sent safely! 🔒✨")
                    .font(.system(size: 12, weight: .bold))
                    .foregroundColor(UsColors.onlineGreen)
                    .padding(.vertical, 8)
            }
        }
        .padding(14)
        .background(
            LinearGradient(
                colors: [Color(red: 0x3A/255.0, green: 0x14/255.0, blue: 0x48/255.0), Color(red: 0x1A/255.0, green: 0x10/255.0, blue: 0x2A/255.0)],
                startPoint: .topLeading,
                endPoint: .bottomTrailing
            )
        )
        .clipShape(RoundedRectangle(cornerRadius: 18))
        .overlay(RoundedRectangle(cornerRadius: 18).stroke(Color.pink.opacity(0.4), lineWidth: 1.5))
        .frame(width: 290)
    }
}
