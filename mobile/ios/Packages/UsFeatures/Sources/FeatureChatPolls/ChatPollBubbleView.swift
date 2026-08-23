import SwiftUI
import UsModel
import UsDesignSystem

public struct ChatPollOption: Identifiable {
    public let id: String
    public let text: String
    public var votesCount: Int
    public var isVotedByMe: Bool

    public init(id: String, text: String, votesCount: Int = 0, isVotedByMe: Bool = false) {
        self.id = id
        self.text = text
        self.votesCount = votesCount
        self.isVotedByMe = isVotedByMe
    }
}

public struct ChatPollBubbleView: View {
    public let question: String
    public let creatorName: String

    @State private var options: [ChatPollOption] = [
        ChatPollOption(id: "cpo-1", text: "Third Wave Coffee ☕️", votesCount: 5, isVotedByMe: true),
        ChatPollOption(id: "cpo-2", text: "Toit Indiranagar 🍕", votesCount: 8, isVotedByMe: false),
        ChatPollOption(id: "cpo-3", text: "CTR Malleshwaram 🥞", votesCount: 3, isVotedByMe: false)
    ]

    public init(
        question: String = "Where should we meet for this weekend's sprint? 📍",
        creatorName: String = "Alex"
    ) {
        self.question = question
        self.creatorName = creatorName
    }

    private var totalVotes: Int {
        options.reduce(0) { $0 + $1.votesCount }
    }

    public var body: some View {
        VStack(alignment: .leading, spacing: 10) {
            HStack(spacing: 6) {
                Image(systemName: "chart.bar.fill")
                    .foregroundColor(UsColors.postbookPrimary)
                Text("Poll by \(creatorName)")
                    .font(.system(size: 11, weight: .bold))
                    .foregroundColor(UsColors.textMuted)
            }

            Text(question)
                .font(.system(size: 14, weight: .bold))
                .foregroundColor(UsColors.textPrimary)

            VStack(spacing: 8) {
                ForEach($options) { $opt in
                    pollOptionRow(option: $opt)
                }
            }

            Text("\(totalVotes) votes cast")
                .font(.system(size: 11))
                .foregroundColor(UsColors.textDim)
        }
        .padding(14)
        .background(UsColors.bgSecondary)
        .clipShape(RoundedRectangle(cornerRadius: 16))
        .overlay(RoundedRectangle(cornerRadius: 16).stroke(UsColors.borderSubtle, lineWidth: 1))
        .frame(width: 290)
    }

    @ViewBuilder
    private func pollOptionRow(option: Binding<ChatPollOption>) -> some View {
        let percentage = totalVotes > 0 ? Double(option.wrappedValue.votesCount) / Double(totalVotes) : 0.0

        Button(action: {
            option.wrappedValue.isVotedByMe.toggle()
            if option.wrappedValue.isVotedByMe {
                option.wrappedValue.votesCount += 1
                HapticManager.shared.trigger(.success)
            } else {
                option.wrappedValue.votesCount = max(0, option.wrappedValue.votesCount - 1)
                HapticManager.shared.trigger(.light)
            }
        }) {
            ZStack(alignment: .leading) {
                RoundedRectangle(cornerRadius: 10)
                    .fill(UsColors.bgTertiary)
                    .frame(height: 38)

                RoundedRectangle(cornerRadius: 10)
                    .fill(option.wrappedValue.isVotedByMe ? UsColors.postbookPrimary.opacity(0.3) : Color.white.opacity(0.1))
                    .frame(width: max(8, 260 * CGFloat(percentage)), height: 38)
                    .animation(.easeOut(duration: 0.3), value: percentage)

                HStack {
                    Text(option.wrappedValue.text)
                        .font(.system(size: 12, weight: .medium))
                        .foregroundColor(UsColors.textPrimary)

                    Spacer()

                    Text(String(format: "%.0f%%", percentage * 100))
                        .font(.system(size: 11, weight: .bold, design: .monospaced))
                        .foregroundColor(UsColors.textMuted)

                    Image(systemName: option.wrappedValue.isVotedByMe ? "checkmark.circle.fill" : "circle")
                        .font(.system(size: 14))
                        .foregroundColor(option.wrappedValue.isVotedByMe ? UsColors.postbookPrimary : UsColors.textDim)
                }
                .padding(.horizontal, 10)
            }
        }
        .buttonStyle(.plain)
    }
}
