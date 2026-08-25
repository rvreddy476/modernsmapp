import SwiftUI
import UsModel
import UsDesignSystem
import UsNetwork

public struct StoryTrayView: View {
    public let userStories: [UserStories]
    public let currentUserId: String
    public let onSelectUserStories: (UserStories) -> Void
    public let onAddStory: () -> Void

    public init(
        userStories: [UserStories],
        currentUserId: String = "me",
        onSelectUserStories: @escaping (UserStories) -> Void,
        onAddStory: @escaping () -> Void = {}
    ) {
        self.userStories = userStories
        self.currentUserId = currentUserId
        self.onSelectUserStories = onSelectUserStories
        self.onAddStory = onAddStory
    }

    public var body: some View {
        ScrollView(.horizontal, showsIndicators: false) {
            LazyHStack(spacing: 16) {
                // Current User Add Story Cell
                addStoryCell

                // Other Users Stories Cells
                ForEach(userStories) { item in
                    storyAvatarCell(item)
                }
            }
            .padding(.horizontal, 16)
            .padding(.vertical, 8)
        }
    }

    private var addStoryCell: some View {
        Button(action: onAddStory) {
            VStack(spacing: 6) {
                ZStack(alignment: .bottomTrailing) {
                    UsAvatar(name: "Your Story", size: .large)

                    Image(systemName: "plus.circle.fill")
                        .font(.system(size: 20))
                        .foregroundColor(UsColors.postbookPrimary)
                        .background(Circle().fill(Color.black))
                        .offset(x: 4, y: 4)
                }

                Text("Your story")
                    .font(.system(size: 11, weight: .regular))
                    .foregroundColor(UsColors.textPrimary)
                    .lineLimit(1)
            }
            .frame(width: 72)
        }
        .buttonStyle(.plain)
    }

    private func storyAvatarCell(_ item: UserStories) -> some View {
        Button(action: { onSelectUserStories(item) }) {
            VStack(spacing: 6) {
                ZStack {
                    // Gradient Ring
                    Circle()
                        .stroke(
                            item.hasUnviewed
                                ? LinearGradient(
                                    colors: [UsColors.postbookPrimary, UsColors.postgramPrimary, UsColors.postgramSecondary],
                                    startPoint: .topLeading,
                                    endPoint: .bottomTrailing
                                )
                                : LinearGradient(
                                    colors: [UsColors.borderMedium, UsColors.borderSubtle],
                                    startPoint: .topLeading,
                                    endPoint: .bottomTrailing
                                ),
                            lineWidth: 2.5
                        )
                        .frame(width: 66, height: 66)

                    UsAvatar(
                        name: item.author.nameForDisplay,
                        url: item.author.avatarUrl,
                        size: .large
                    )
                }

                Text(item.author.nameForDisplay)
                    .font(.system(size: 11, weight: .regular))
                    .foregroundColor(UsColors.textPrimary)
                    .lineLimit(1)
            }
            .frame(width: 72)
        }
        .buttonStyle(.plain)
    }
}
