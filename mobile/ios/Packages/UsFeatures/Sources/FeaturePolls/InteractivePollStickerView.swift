import SwiftUI
import UsModel
import UsDesignSystem

public struct PollOptionItem: Identifiable {
    public let id: String
    public let text: String
    public var votes: Int

    public init(id: String, text: String, votes: Int = 0) {
        self.id = id
        self.text = text
        self.votes = votes
    }
}

public struct InteractivePollStickerView: View {
    public let question: String
    public let onVoted: (String) -> Void

    @State private var options: [PollOptionItem]
    @State private var selectedOptionId: String? = nil

    public init(
        question: String = "What's the best biryani in India? 🍚",
        options: [PollOptionItem] = [
            PollOptionItem(id: "opt-1", text: "Hyderabadi Dum", votes: 420),
            PollOptionItem(id: "opt-2", text: "Lucknowi Awadhi", votes: 260),
            PollOptionItem(id: "opt-3", text: "Kolkata with Aloo", votes: 190)
        ],
        onVoted: @escaping (String) -> Void = { _ in }
    ) {
        self.question = question
        self._options = State(initialValue: options)
        self.onVoted = onVoted
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
                    optionRow(opt)
                }
            }

            Text("\(totalVotes) votes • Live Poll")
                .font(.system(size: 11))
                .foregroundColor(.white.opacity(0.7))
        }
        .padding(16)
        .background(Color.black.opacity(0.8))
        .clipShape(RoundedRectangle(cornerRadius: 20))
        .overlay(RoundedRectangle(cornerRadius: 20).stroke(Color.white.opacity(0.2), lineWidth: 1))
        .frame(width: 280)
    }

    @ViewBuilder
    private func optionRow(_ opt: PollOptionItem) -> some View {
        let isSelected = selectedOptionId == opt.id
        let percentage = totalVotes > 0 ? Double(opt.votes) / Double(totalVotes) : 0.0

        Button(action: {
            guard selectedOptionId == nil else { return }
            selectedOptionId = opt.id
            if let idx = options.firstIndex(where: { $0.id == opt.id }) {
                options[idx].votes += 1
            }
            HapticManager.shared.trigger(.success)
            onVoted(opt.id)
        }) {
            ZStack(alignment: .leading) {
                // Background Track
                RoundedRectangle(cornerRadius: 12)
                    .fill(Color.white.opacity(0.15))
                    .frame(height: 40)

                // Fill Percentage Bar if voted
                if selectedOptionId != nil {
                    RoundedRectangle(cornerRadius: 12)
                        .fill(isSelected ? UsColors.postbookPrimary : Color.white.opacity(0.3))
                        .frame(width: max(12, 248 * CGFloat(percentage)), height: 40)
                        .animation(.easeOut(duration: 0.4), value: percentage)
                }

                // Text & Percentage
                HStack {
                    Text(opt.text)
                        .font(.system(size: 13, weight: .semibold))
                        .foregroundColor(.white)

                    Spacer()

                    if selectedOptionId != nil {
                        Text(String(format: "%.0f%%", percentage * 100))
                            .font(.system(size: 13, weight: .bold, design: .monospaced))
                            .foregroundColor(.white)
                    }
                }
                .padding(.horizontal, 14)
            }
        }
        .buttonStyle(.plain)
        .disabled(selectedOptionId != nil)
    }
}
