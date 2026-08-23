import SwiftUI
import UsModel
import UsDesignSystem

public struct QuizOptionItem: Identifiable {
    public let id: String
    public let text: String
    public let isCorrect: Bool
    public var votes: Int

    public init(id: String, text: String, isCorrect: Bool, votes: Int = 0) {
        self.id = id
        self.text = text
        self.isCorrect = isCorrect
        self.votes = votes
    }
}

public struct QuizStickerView: View {
    public let question: String
    public let onAnswer: (Bool) -> Void

    @State private var options: [QuizOptionItem]
    @State private var selectedOptionId: String? = nil

    public init(
        question: String = "What year was the first UPI transaction made? 🇮🇳",
        options: [QuizOptionItem] = [
            QuizOptionItem(id: "qo-1", text: "A. 2014", isCorrect: false, votes: 120),
            QuizOptionItem(id: "qo-2", text: "B. 2016", isCorrect: true, votes: 680),
            QuizOptionItem(id: "qo-3", text: "C. 2018", isCorrect: false, votes: 90),
            QuizOptionItem(id: "qo-4", text: "D. 2020", isCorrect: false, votes: 40)
        ],
        onAnswer: @escaping (Bool) -> Void = { _ in }
    ) {
        self.question = question
        self._options = State(initialValue: options)
        self.onAnswer = onAnswer
    }

    private var totalVotes: Int {
        options.reduce(0) { $0 + $1.votes }
    }

    public var body: some View {
        VStack(spacing: 12) {
            Text(question)
                .font(.system(size: 15, weight: .bold))
                .foregroundColor(.white)
                .multilineTextAlignment(.center)
                .padding(.horizontal, 8)

            VStack(spacing: 8) {
                ForEach(options) { opt in
                    quizOptionRow(opt)
                }
            }

            Text("\(totalVotes) responses • Story Quiz")
                .font(.system(size: 11))
                .foregroundColor(.white.opacity(0.7))
        }
        .padding(16)
        .background(Color.black.opacity(0.85))
        .clipShape(RoundedRectangle(cornerRadius: 20))
        .overlay(RoundedRectangle(cornerRadius: 20).stroke(Color.white.opacity(0.2), lineWidth: 1))
        .frame(width: 280)
    }

    @ViewBuilder
    private func quizOptionRow(_ opt: QuizOptionItem) -> some View {
        let isSelected = selectedOptionId == opt.id
        let isRevealed = selectedOptionId != nil
        let isCorrect = opt.isCorrect

        Button(action: {
            guard selectedOptionId == nil else { return }
            selectedOptionId = opt.id
            if let idx = options.firstIndex(where: { $0.id == opt.id }) {
                options[idx].votes += 1
            }
            if opt.isCorrect {
                HapticManager.shared.trigger(.success)
            } else {
                HapticManager.shared.trigger(.error)
            }
            onAnswer(opt.isCorrect)
        }) {
            HStack {
                Text(opt.text)
                    .font(.system(size: 13, weight: .semibold))
                    .foregroundColor(isRevealed && isCorrect ? .black : (isSelected && !isCorrect ? .white : UsColors.textPrimary))

                Spacer()

                if isRevealed {
                    if isCorrect {
                        Image(systemName: "checkmark.circle.fill")
                            .foregroundColor(.black)
                    } else if isSelected {
                        Image(systemName: "xmark.circle.fill")
                            .foregroundColor(.white)
                    }
                }
            }
            .padding(.horizontal, 14)
            .padding(.vertical, 10)
            .background(
                isRevealed ?
                    (isCorrect ? UsColors.onlineGreen : (isSelected ? UsColors.liveRed : UsColors.bgTertiary)) :
                    UsColors.bgSecondary
            )
            .clipShape(RoundedRectangle(cornerRadius: 12))
            .overlay(
                RoundedRectangle(cornerRadius: 12)
                    .stroke(isSelected ? Color.white : UsColors.borderSubtle, lineWidth: 1)
            )
        }
        .buttonStyle(.plain)
        .disabled(selectedOptionId != nil)
    }
}
