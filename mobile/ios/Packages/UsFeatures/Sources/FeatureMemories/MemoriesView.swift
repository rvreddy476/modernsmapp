import SwiftUI
import UsModel
import UsDesignSystem
import UsNetwork

public struct MemoryItem: Identifiable, Hashable {
    public let id: String
    public let yearsAgo: Int
    public let dateString: String
    public let text: String
    public let imageUrl: String
}

public struct MemoriesView: View {
    public let onDismiss: () -> Void

    private let memories: [MemoryItem] = [
        MemoryItem(id: "m1", yearsAgo: 2, dateString: "August 21, 2024 (2 Years Ago Today)", text: "Sunset trek at Nandi Hills with the team 🌅", imageUrl: "https://images.unsplash.com/photo-1506744038136-46273834b3fb?w=800"),
        MemoryItem(id: "m2", yearsAgo: 1, dateString: "August 21, 2025 (1 Year Ago Today)", text: "First prototype launch! What a wild milestone 🚀", imageUrl: "https://images.unsplash.com/photo-1519389950473-47ba0277781c?w=800")
    ]

    public init(onDismiss: @escaping () -> Void = {}) {
        self.onDismiss = onDismiss
    }

    public var body: some View {
        NavigationStack {
            ZStack {
                UsColors.bgPrimary
                    .ignoresSafeArea()

                ScrollView {
                    VStack(alignment: .leading, spacing: 20) {
                        Text("On This Day")
                            .font(.system(size: 22, weight: .bold))
                            .foregroundColor(UsColors.textPrimary)

                        ForEach(memories) { memory in
                            memoryCard(memory)
                        }
                    }
                    .padding(16)
                }
            }
            .navigationTitle("Memories")
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
    private func memoryCard(_ memory: MemoryItem) -> some View {
        VStack(alignment: .leading, spacing: 12) {
            // Header
            HStack(spacing: 8) {
                Image(systemName: "sparkles")
                    .foregroundColor(UsColors.postgramPrimary)
                Text(memory.dateString)
                    .font(.system(size: 13, weight: .semibold))
                    .foregroundColor(UsColors.postgramPrimary)
            }

            if let url = URL(string: memory.imageUrl) {
                AsyncImage(url: url) { phase in
                    switch phase {
                    case .success(let img):
                        img.resizable().scaledToFill()
                    default:
                        Rectangle().fill(UsColors.bgTertiary)
                    }
                }
                .frame(height: 240)
                .clipShape(RoundedRectangle(cornerRadius: 14))
            }

            Text(memory.text)
                .font(.system(size: 15))
                .foregroundColor(UsColors.textPrimary)

            HStack {
                Button(action: {
                    ToastManager.shared.show("Memory shared to your Story!", style: .success)
                    onDismiss()
                }) {
                    HStack(spacing: 6) {
                        Image(systemName: "plus.circle.fill")
                        Text("Share to Story")
                    }
                    .font(.system(size: 13, weight: .bold))
                    .foregroundColor(.black)
                    .padding(.horizontal, 16)
                    .padding(.vertical, 10)
                    .background(Color.white)
                    .clipShape(Capsule())
                }

                Spacer()
            }
            .padding(.top, 4)
        }
        .padding(16)
        .background(UsColors.bgSecondary)
        .clipShape(RoundedRectangle(cornerRadius: 18))
    }
}
