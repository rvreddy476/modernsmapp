import SwiftUI

public struct PollOption: Identifiable, Hashable, Codable, Sendable {
    public let id: String
    public let text: String
    public var votesCount: Int

    public init(id: String = UUID().uuidString, text: String, votesCount: Int = 0) {
        self.id = id
        self.text = text
        self.votesCount = votesCount
    }
}

public struct PollWidgetView: View {
    public let question: String
    @State public var options: [PollOption]
    @State private var votedOptionId: String? = nil

    public init(question: String, options: [PollOption]) {
        self.question = question
        self._options = State(initialValue: options)
    }

    private var totalVotes: Int {
        options.reduce(0) { $0 + $1.votesCount }
    }

    public var body: some View {
        VStack(alignment: .leading, spacing: 10) {
            Text(question)
                .font(.system(size: 15, weight: .bold))
                .foregroundColor(UsColors.textPrimary)

            VStack(spacing: 8) {
                ForEach(options) { option in
                    pollOptionRow(option)
                }
            }

            Text("\(totalVotes) votes")
                .font(.system(size: 12))
                .foregroundColor(UsColors.textMuted)
        }
        .padding(14)
        .background(UsColors.bgSecondary)
        .clipShape(RoundedRectangle(cornerRadius: 14))
        .overlay(RoundedRectangle(cornerRadius: 14).stroke(UsColors.borderSubtle, lineWidth: 1))
    }

    @ViewBuilder
    private func pollOptionRow(_ option: PollOption) -> some View {
        let hasVoted = votedOptionId != nil
        let isSelected = votedOptionId == option.id
        let percent = totalVotes > 0 ? Double(option.votesCount) / Double(totalVotes) : 0.0

        Button(action: {
            guard votedOptionId == nil else { return }
            votedOptionId = option.id
            if let idx = options.firstIndex(where: { $0.id == option.id }) {
                options[idx].votesCount += 1
            }
            HapticManager.shared.trigger(.selection)
        }) {
            ZStack(alignment: .leading) {
                // Background track
                RoundedRectangle(cornerRadius: 10)
                    .fill(UsColors.bgTertiary)
                    .frame(height: 44)

                // Fill bar if voted
                if hasVoted {
                    GeometryReader { geo in
                        RoundedRectangle(cornerRadius: 10)
                            .fill(isSelected ? UsColors.postbookPrimary.opacity(0.3) : Color.white.opacity(0.1))
                            .frame(width: geo.size.width * CGFloat(percent))
                    }
                    .frame(height: 44)
                }

                // Text & percentage
                HStack {
                    Text(option.text)
                        .font(.system(size: 14, weight: isSelected ? .bold : .medium))
                        .foregroundColor(UsColors.textPrimary)

                    Spacer()

                    if hasVoted {
                        Text(String(format: "%.0f%%", percent * 100))
                            .font(.system(size: 13, weight: .bold, design: .rounded))
                            .foregroundColor(UsColors.textPrimary)
                    }
                }
                .padding(.horizontal, 14)
            }
        }
        .buttonStyle(.plain)
    }
}
