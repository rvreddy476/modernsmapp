import SwiftUI
import UsModel
import UsDesignSystem

public struct TriviaQuestion {
    public let question: String
    public let options: [String]
    public let correctIndex: Int
}

public struct LiveTriviaGameView: View {
    public let onDismiss: () -> Void

    @State private var currentQuestionIndex: Int = 0
    @State private var selectedOptionIndex: Int? = nil
    @State private var countdown: Int = 10
    @State private var userScore: Int = 0
    @State private var isGameOver: Bool = false

    private let questions: [TriviaQuestion] = [
        TriviaQuestion(
            question: "Which city is known as the Silicon Valley of India?",
            options: ["Hyderabad", "Bengaluru", "Pune", "Gurugram"],
            correctIndex: 1
        ),
        TriviaQuestion(
            question: "What is India's real-time payment system called?",
            options: ["UPI", "FedNow", "Pix", "SEPA"],
            correctIndex: 0
        ),
        TriviaQuestion(
            question: "Which Indian festival is known as the Festival of Lights?",
            options: ["Holi", "Diwali", "Eid", "Navratri"],
            correctIndex: 1
        )
    ]

    public init(onDismiss: @escaping () -> Void = {}) {
        self.onDismiss = onDismiss
    }

    public var body: some View {
        NavigationStack {
            ZStack {
                UsColors.bgPrimary
                    .ignoresSafeArea()

                VStack(spacing: 20) {
                    if isGameOver {
                        gameOverSummary
                    } else {
                        // Header info & countdown
                        HStack {
                            Text("Question \(currentQuestionIndex + 1) of \(questions.count)")
                                .font(.system(size: 13, weight: .bold))
                                .foregroundColor(UsColors.textMuted)

                            Spacer()

                            HStack(spacing: 4) {
                                Image(systemName: "timer")
                                Text("\(countdown)s")
                                    .font(.system(size: 14, weight: .black, design: .monospaced))
                            }
                            .foregroundColor(countdown <= 3 ? UsColors.liveRed : UsColors.postbookPrimary)
                            .padding(.horizontal, 10)
                            .padding(.vertical, 4)
                            .background(UsColors.bgSecondary)
                            .clipShape(Capsule())
                        }
                        .padding(.horizontal, 16)

                        // Question Card
                        let currentQ = questions[currentQuestionIndex]
                        VStack(spacing: 12) {
                            Text(currentQ.question)
                                .font(.system(size: 18, weight: .bold))
                                .foregroundColor(UsColors.textPrimary)
                                .multilineTextAlignment(.center)
                                .padding(.horizontal, 12)
                        }
                        .frame(maxWidth: .infinity)
                        .padding(.vertical, 24)
                        .background(UsColors.bgSecondary)
                        .clipShape(RoundedRectangle(cornerRadius: 18))
                        .padding(.horizontal, 16)

                        // Options
                        VStack(spacing: 10) {
                            ForEach(Array(currentQ.options.enumerated()), id: \.offset) { idx, opt in
                                triviaOptionRow(text: opt, index: idx, correctIndex: currentQ.correctIndex)
                            }
                        }
                        .padding(.horizontal, 16)

                        Spacer()
                    }
                }
                .padding(.top, 12)
            }
            .navigationTitle("Live Trivia Battle 🧠")
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
    private func triviaOptionRow(text: String, index: Int, correctIndex: Int) -> some View {
        let isSelected = selectedOptionIndex == index
        let isCorrect = selectedOptionIndex != nil && index == correctIndex
        let isWrong = isSelected && index != correctIndex

        Button(action: {
            guard selectedOptionIndex == nil else { return }
            selectedOptionIndex = index
            if index == correctIndex {
                userScore += 100
                HapticManager.shared.trigger(.success)
            } else {
                HapticManager.shared.trigger(.error)
            }

            DispatchQueue.main.asyncAfter(deadline: .now() + 1.2) {
                if currentQuestionIndex < questions.count - 1 {
                    currentQuestionIndex += 1
                    selectedOptionIndex = nil
                    countdown = 10
                } else {
                    isGameOver = true
                }
            }
        }) {
            HStack {
                Text(text)
                    .font(.system(size: 15, weight: .semibold))
                    .foregroundColor(isWrong ? .white : (isCorrect ? .black : UsColors.textPrimary))

                Spacer()

                if isCorrect {
                    Image(systemName: "checkmark.circle.fill")
                        .foregroundColor(.black)
                } else if isWrong {
                    Image(systemName: "xmark.circle.fill")
                        .foregroundColor(.white)
                }
            }
            .padding(16)
            .background(isCorrect ? UsColors.onlineGreen : (isWrong ? UsColors.liveRed : UsColors.bgSecondary))
            .clipShape(RoundedRectangle(cornerRadius: 14))
            .overlay(
                RoundedRectangle(cornerRadius: 14)
                    .stroke(isSelected ? Color.white : UsColors.borderSubtle, lineWidth: isSelected ? 2 : 1)
            )
        }
        .buttonStyle(.plain)
        .disabled(selectedOptionIndex != nil)
    }

    private var gameOverSummary: some View {
        VStack(spacing: 20) {
            Spacer()

            VStack(spacing: 10) {
                Text("🏆")
                    .font(.system(size: 54))

                Text("Tournament Finished!")
                    .font(.system(size: 22, weight: .bold))
                    .foregroundColor(UsColors.textPrimary)

                Text("Your Score: \(userScore) Points")
                    .font(.system(size: 20, weight: .black, design: .rounded))
                    .foregroundColor(UsColors.onlineGreen)
            }

            Spacer()

            Button(action: onDismiss) {
                HStack {
                    Spacer()
                    Text("Collect Prize & Exit")
                        .font(.system(size: 15, weight: .bold))
                        .foregroundColor(.black)
                    Spacer()
                }
                .padding(.vertical, 16)
                .background(Color.white)
                .clipShape(RoundedRectangle(cornerRadius: 14))
            }
            .padding(16)
        }
    }
}
