import SwiftUI
import UsModel
import UsDesignSystem
import UsNetwork

@Observable
public final class QAFeedViewModel: @unchecked Sendable {
    public var questions: [QuestionItem] = []
    public var selectedTopic: String = "All"
    public var upvotedQuestionIds: Set<String> = []
    public var showAskSheet: Bool = false

    public let topics = ["All", "Tech & AI", "Startups", "Bangalore Life", "Design", "Finance"]

    public init() {
        populateMockQuestions()
    }

    public var filteredQuestions: [QuestionItem] {
        if selectedTopic == "All" { return questions }
        return questions.filter { $0.topic == selectedTopic }
    }

    public func toggleUpvote(questionId: String) {
        if upvotedQuestionIds.contains(questionId) {
            upvotedQuestionIds.remove(questionId)
        } else {
            upvotedQuestionIds.insert(questionId)
            HapticManager.shared.trigger(.selection)
        }
    }

    private func populateMockQuestions() {
        let a1 = AnswerItem(
            id: "ans-1",
            author: Author(id: "u-exp1", username: "arjun_tech", displayName: "Arjun Nambiar"),
            text: "Swift 6 strict concurrency requires thinking in isolated actors. Always isolate your ViewModels with @MainActor and use @Observable instead of ObservableObject.",
            upvotes: 89,
            isVerifiedExpert: true
        )
        let q1 = QuestionItem(
            id: "q1",
            title: "What is the best way to handle Swift 6 data race safety in large iOS codebases?",
            author: Author(id: "u1", username: "dev_riya", displayName: "Riya Sen"),
            topic: "Tech & AI",
            upvotesCount: 142,
            answersCount: 12,
            topAnswer: a1
        )

        let a2 = AnswerItem(
            id: "ans-2",
            author: Author(id: "u-exp2", username: "blt_foodie", displayName: "Bangalore Food Guide"),
            text: "Go to Taaza Thindi in Jayanagar early morning for the crispiest masala dosa and filter coffee under ₹50.",
            upvotes: 210,
            isVerifiedExpert: true
        )
        let q2 = QuestionItem(
            id: "q2",
            title: "Which South Bangalore spot has the most authentic butter masala dosa?",
            author: Author(id: "u2", username: "traveler_karan", displayName: "Karan Johar"),
            topic: "Bangalore Life",
            upvotesCount: 320,
            answersCount: 45,
            topAnswer: a2
        )

        questions = [q1, q2]
    }
}

public struct QAFeedView: View {
    @State private var viewModel = QAFeedViewModel()
    @State private var askQuestionTitle: String = ""
    public let onDismiss: () -> Void

    public init(onDismiss: @escaping () -> Void = {}) {
        self.onDismiss = onDismiss
    }

    public var body: some View {
        NavigationStack {
            ZStack {
                UsColors.bgPrimary
                    .ignoresSafeArea()

                ScrollView {
                    VStack(alignment: .leading, spacing: 18) {
                        // Ask Prompt Bar
                        askPromptBar

                        // Topics Filter
                        topicsFilterRow

                        // Questions Feed
                        LazyVStack(spacing: 16) {
                            ForEach(viewModel.filteredQuestions) { q in
                                questionCard(q)
                            }
                        }
                    }
                    .padding(16)
                }
            }
            .navigationTitle("Q&A Discussions")
            .navigationBarTitleDisplayMode(.inline)
            .toolbar {
                ToolbarItem(placement: .cancellationAction) {
                    Button("Close", action: onDismiss)
                        .foregroundColor(UsColors.textMuted)
                }
            }
            .sheet(isPresented: $viewModel.showAskSheet) {
                askQuestionSheet
            }
        }
    }

    private var askPromptBar: some View {
        Button(action: { viewModel.showAskSheet = true }) {
            HStack(spacing: 12) {
                Image(systemName: "questionmark.circle.fill")
                    .font(.system(size: 24))
                    .foregroundColor(UsColors.postbookPrimary)

                Text("What do you want to ask or share?")
                    .font(.system(size: 14))
                    .foregroundColor(UsColors.textMuted)

                Spacer()
            }
            .padding(14)
            .background(UsColors.bgSecondary)
            .clipShape(RoundedRectangle(cornerRadius: 14))
            .overlay(RoundedRectangle(cornerRadius: 14).stroke(UsColors.borderMedium, lineWidth: 1))
        }
        .buttonStyle(.plain)
    }

    private var topicsFilterRow: some View {
        ScrollView(.horizontal, showsIndicators: false) {
            HStack(spacing: 8) {
                ForEach(viewModel.topics, id: \.self) { topic in
                    let isSelected = viewModel.selectedTopic == topic
                    Button(action: { viewModel.selectedTopic = topic }) {
                        Text(topic)
                            .font(.system(size: 13, weight: .semibold))
                            .foregroundColor(isSelected ? .black : UsColors.textPrimary)
                            .padding(.horizontal, 16)
                            .padding(.vertical, 8)
                            .background(isSelected ? Color.white : UsColors.bgSecondary)
                            .clipShape(Capsule())
                    }
                    .buttonStyle(.plain)
                }
            }
        }
    }

    @ViewBuilder
    private func questionCard(_ q: QuestionItem) -> some View {
        VStack(alignment: .leading, spacing: 12) {
            // Topic & Author
            HStack(spacing: 8) {
                Text(q.topic)
                    .font(.system(size: 11, weight: .bold))
                    .foregroundColor(UsColors.postbookPrimary)
                    .padding(.horizontal, 8)
                    .padding(.vertical, 3)
                    .background(UsColors.postbookPrimary.opacity(0.15))
                    .clipShape(RoundedRectangle(cornerRadius: 4))

                Text("Asked by \(q.author.nameForDisplay) • \(q.createdAt)")
                    .font(.system(size: 12))
                    .foregroundColor(UsColors.textMuted)

                Spacer()
            }

            // Question Title
            Text(q.title)
                .font(.system(size: 16, weight: .bold))
                .foregroundColor(UsColors.textPrimary)
                .lineSpacing(2)

            // Top Answer Highlight
            if let ans = q.topAnswer {
                VStack(alignment: .leading, spacing: 6) {
                    HStack(spacing: 6) {
                        UsAvatar(name: ans.author.nameForDisplay, size: .small)
                        Text(ans.author.nameForDisplay)
                            .font(.system(size: 13, weight: .bold))
                            .foregroundColor(UsColors.textPrimary)

                        if ans.isVerifiedExpert {
                            HStack(spacing: 2) {
                                Image(systemName: "checkmark.seal.fill")
                                Text("Expert")
                            }
                            .font(.system(size: 10, weight: .bold))
                            .foregroundColor(UsColors.onlineGreen)
                        }
                    }

                    Text(ans.text)
                        .font(.system(size: 13))
                        .foregroundColor(UsColors.textSecondary)
                        .lineLimit(3)
                        .lineSpacing(2)
                }
                .padding(12)
                .background(UsColors.bgTertiary)
                .clipShape(RoundedRectangle(cornerRadius: 10))
            }

            // Action Bar: Upvote count + Answers count + Share
            HStack(spacing: 16) {
                Button(action: { viewModel.toggleUpvote(questionId: q.id) }) {
                    let hasUpvoted = viewModel.upvotedQuestionIds.contains(q.id)
                    HStack(spacing: 6) {
                        Image(systemName: hasUpvoted ? "arrow.up.circle.fill" : "arrow.up.circle")
                            .font(.system(size: 16))
                            .foregroundColor(hasUpvoted ? UsColors.postbookPrimary : UsColors.textMuted)
                        Text("\(q.upvotesCount + (hasUpvoted ? 1 : 0)) Upvotes")
                            .font(.system(size: 13, weight: .medium))
                            .foregroundColor(hasUpvoted ? UsColors.postbookPrimary : UsColors.textPrimary)
                    }
                }
                .buttonStyle(.plain)

                HStack(spacing: 6) {
                    Image(systemName: "bubble.left")
                        .font(.system(size: 15))
                        .foregroundColor(UsColors.textMuted)
                    Text("\(q.answersCount) Answers")
                        .font(.system(size: 13, weight: .medium))
                        .foregroundColor(UsColors.textPrimary)
                }

                Spacer()
            }
            .padding(.top, 4)
        }
        .padding(16)
        .background(UsColors.bgSecondary)
        .clipShape(RoundedRectangle(cornerRadius: 16))
    }

    private var askQuestionSheet: some View {
        NavigationStack {
            ZStack {
                UsColors.bgPrimary.ignoresSafeArea()

                VStack(spacing: 16) {
                    TextEditor(text: $askQuestionTitle)
                        .scrollContentBackground(.hidden)
                        .background(UsColors.bgSecondary)
                        .foregroundColor(UsColors.textPrimary)
                        .font(.system(size: 16))
                        .padding(12)
                        .clipShape(RoundedRectangle(cornerRadius: 12))
                        .frame(height: 140)

                    Spacer()

                    Button(action: {
                        viewModel.showAskSheet = false
                        askQuestionTitle = ""
                        ToastManager.shared.show("Question posted to Q&A community!", style: .success)
                    }) {
                        HStack {
                            Spacer()
                            Text("Post Question")
                                .font(.system(size: 15, weight: .bold))
                                .foregroundColor(.black)
                            Spacer()
                        }
                        .padding(.vertical, 16)
                        .background(Color.white)
                        .clipShape(RoundedRectangle(cornerRadius: 14))
                    }
                    .disabled(askQuestionTitle.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty)
                }
                .padding(16)
            }
            .navigationTitle("Ask Question")
            .navigationBarTitleDisplayMode(.inline)
            .toolbar {
                ToolbarItem(placement: .cancellationAction) {
                    Button("Cancel") { viewModel.showAskSheet = false }
                        .foregroundColor(UsColors.textMuted)
                }
            }
        }
    }
}
