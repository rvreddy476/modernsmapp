import SwiftUI
import UsModel
import UsDesignSystem

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

    @State private var messages: [SecretChatMessage] = [
        SecretChatMessage(id: "sm-1", text: "Hey! This chat is encrypted on-device with zero server logs. 🔐", isSender: false, remainingSeconds: 8),
        SecretChatMessage(id: "sm-2", text: "Awesome, messages self-destruct after viewing.", isSender: true, remainingSeconds: 10)
    ]
    @State private var inputText: String = ""
    @State private var timerDuration: Int = 10

    public init(
        recipientName: String = "Sarah Chen",
        onDismiss: @escaping () -> Void = {}
    ) {
        self.recipientName = recipientName
        self.onDismiss = onDismiss
    }

    public var body: some View {
        NavigationStack {
            ZStack {
                Color.black.ignoresSafeArea()

                VStack(spacing: 0) {
                    // Privacy indicator ribbon
                    HStack(spacing: 6) {
                        Image(systemName: "lock.fill")
                            .font(.system(size: 11))
                            .foregroundColor(UsColors.onlineGreen)

                        Text("Secret Chat • Auto-Destruct in \(timerDuration)s")
                            .font(.system(size: 11, weight: .semibold))
                            .foregroundColor(.white.opacity(0.8))
                    }
                    .padding(.vertical, 6)
                    .frame(maxWidth: .infinity)
                    .background(Color(red: 0x14/255.0, green: 0x1E/255.0, blue: 0x14/255.0))

                    // Messages list
                    ScrollView {
                        LazyVStack(spacing: 12) {
                            ForEach(messages) { msg in
                                secretBubble(msg)
                            }
                        }
                        .padding(16)
                    }

                    // Input bar
                    HStack(spacing: 8) {
                        TextField("Type encrypted message...", text: $inputText)
                            .textFieldStyle(.plain)
                            .font(.system(size: 14))
                            .foregroundColor(.white)
                            .padding(.horizontal, 14)
                            .padding(.vertical, 10)
                            .background(Color(red: 0x1E/255.0, green: 0x1E/255.0, blue: 0x2A/255.0))
                            .clipShape(Capsule())

                        Button(action: sendMessage) {
                            Image(systemName: "arrow.up.circle.fill")
                                .font(.system(size: 32))
                                .foregroundColor(inputText.isEmpty ? Color.gray : UsColors.onlineGreen)
                        }
                        .disabled(inputText.isEmpty)
                    }
                    .padding(.horizontal, 16)
                    .padding(.vertical, 12)
                    .background(Color(red: 0x12/255.0, green: 0x12/255.0, blue: 0x18/255.0))
                }
            }
            .navigationTitle("Secret: \(recipientName)")
            .navigationBarTitleDisplayMode(.inline)
            .toolbar {
                ToolbarItem(placement: .cancellationAction) {
                    Button("Close", action: onDismiss)
                        .foregroundColor(UsColors.textMuted)
                }
            }
            .onAppear {
                startCountdownTicker()
            }
        }
    }

    @ViewBuilder
    private func secretBubble(_ msg: SecretChatMessage) -> some View {
        HStack {
            if msg.isSender { Spacer() }

            VStack(alignment: msg.isSender ? .trailing : .leading, spacing: 4) {
                Text(msg.text)
                    .font(.system(size: 14))
                    .foregroundColor(.white)

                HStack(spacing: 4) {
                    Image(systemName: "flame.fill")
                        .font(.system(size: 9))
                        .foregroundColor(Color.orange)
                    Text("\(msg.remainingSeconds)s")
                        .font(.system(size: 10, weight: .bold, design: .monospaced))
                        .foregroundColor(.white.opacity(0.6))
                }
            }
            .padding(.horizontal, 14)
            .padding(.vertical, 10)
            .background(
                msg.isSender ?
                    Color(red: 0x1E/255.0, green: 0x4A/255.0, blue: 0x2E/255.0) :
                    Color(red: 0x1E/255.0, green: 0x1E/255.0, blue: 0x2A/255.0)
            )
            .clipShape(RoundedRectangle(cornerRadius: 16))

            if !msg.isSender { Spacer() }
        }
    }

    private func sendMessage() {
        guard !inputText.isEmpty else { return }
        HapticManager.shared.trigger(.light)
        let newMsg = SecretChatMessage(id: UUID().uuidString, text: inputText, isSender: true, remainingSeconds: timerDuration)
        messages.append(newMsg)
        inputText = ""
    }

    private func startCountdownTicker() {
        Timer.scheduledTimer(withTimeInterval: 1.0, repeats: true) { _ in
            for i in messages.indices {
                if messages[i].remainingSeconds > 0 {
                    messages[i].remainingSeconds -= 1
                }
            }
            messages.removeAll { $0.remainingSeconds <= 0 }
        }
    }
}
