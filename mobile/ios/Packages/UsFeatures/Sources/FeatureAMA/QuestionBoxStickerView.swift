import SwiftUI
import UsModel
import UsDesignSystem

public struct QuestionBoxStickerView: View {
    public let promptTitle: String
    public let recipientName: String
    public let onSubmitAnswer: (String) -> Void

    @State private var answerText: String = ""
    @State private var isSubmitted: Bool = false

    public init(
        promptTitle: String = "Ask me anything! ✨",
        recipientName: String = "Sarah",
        onSubmitAnswer: @escaping (String) -> Void = { _ in }
    ) {
        self.promptTitle = promptTitle
        self.recipientName = recipientName
        self.onSubmitAnswer = onSubmitAnswer
    }

    public var body: some View {
        VStack(spacing: 12) {
            // Header prompt
            VStack(spacing: 4) {
                Text(promptTitle)
                    .font(.system(size: 16, weight: .bold))
                    .foregroundColor(Color.black)
                Text("Send a question to @\(recipientName)")
                    .font(.system(size: 11))
                    .foregroundColor(Color.black.opacity(0.6))
            }
            .padding(.top, 4)

            // Input field
            if isSubmitted {
                HStack(spacing: 6) {
                    Image(systemName: "checkmark.circle.fill")
                        .foregroundColor(UsColors.onlineGreen)
                    Text("Sent!")
                        .font(.system(size: 14, weight: .bold))
                        .foregroundColor(Color.black)
                }
                .frame(maxWidth: .infinity)
                .padding(.vertical, 14)
                .background(Color.white)
                .clipShape(RoundedRectangle(cornerRadius: 12))
            } else {
                HStack {
                    TextField("Type something...", text: $answerText)
                        .textFieldStyle(.plain)
                        .font(.system(size: 13))
                        .foregroundColor(.black)

                    Button(action: sendAnswer) {
                        Image(systemName: "arrow.up.circle.fill")
                            .font(.system(size: 24))
                            .foregroundColor(answerText.isEmpty ? Color.gray : Color.black)
                    }
                    .disabled(answerText.isEmpty)
                }
                .padding(.horizontal, 12)
                .padding(.vertical, 10)
                .background(Color.white)
                .clipShape(RoundedRectangle(cornerRadius: 12))
            }
        }
        .padding(16)
        .background(
            LinearGradient(
                colors: [Color(red: 0xFF/255.0, green: 0x9A/255.0, blue: 0x8B/255.0), Color(red: 0xFF/255.0, green: 0x6A/255.0, blue: 0x88/255.0)],
                startPoint: .topLeading,
                endPoint: .bottomTrailing
            )
        )
        .clipShape(RoundedRectangle(cornerRadius: 20))
        .shadow(radius: 8)
        .frame(width: 270)
    }

    private func sendAnswer() {
        guard !answerText.isEmpty else { return }
        isSubmitted = true
        HapticManager.shared.trigger(.success)
        onSubmitAnswer(answerText)
        ToastManager.shared.show("Question sent to \(recipientName)!", style: .success)
    }
}
