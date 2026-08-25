import SwiftUI
import UsModel
import UsDesignSystem

public struct MentionStickerView: View {
    public let user: Author
    public let onProfileTapped: (Author) -> Void

    @State private var isShowingPreview: Bool = false
    @State private var isFollowing: Bool = false

    public init(
        user: Author = Author(id: "u-1", username: "sarah_c", displayName: "Sarah Chen"),
        onProfileTapped: @escaping (Author) -> Void = { _ in }
    ) {
        self.user = user
        self.onProfileTapped = onProfileTapped
    }

    public var body: some View {
        Button(action: {
            isShowingPreview.toggle()
            HapticManager.shared.trigger(.selection)
        }) {
            HStack(spacing: 6) {
                Text("@\(user.username)")
                    .font(.system(size: 14, weight: .bold))
                    .foregroundColor(.white)
            }
            .padding(.horizontal, 14)
            .padding(.vertical, 8)
            .background(Color.black.opacity(0.75))
            .clipShape(Capsule())
            .overlay(Capsule().stroke(Color.white.opacity(0.3), lineWidth: 1))
        }
        .buttonStyle(.plain)
        .popover(isPresented: $isShowingPreview) {
            previewCard
        }
    }

    private var previewCard: some View {
        VStack(spacing: 12) {
            UsAvatar(name: user.nameForDisplay, url: user.avatarUrl, size: .large)

            VStack(spacing: 2) {
                Text(user.nameForDisplay)
                    .font(.system(size: 16, weight: .bold))
                    .foregroundColor(UsColors.textPrimary)
                Text("@\(user.username)")
                    .font(.system(size: 12))
                    .foregroundColor(UsColors.textMuted)
            }

            Text("Creator • 48.2K Followers")
                .font(.system(size: 11, weight: .medium))
                .foregroundColor(UsColors.postbookPrimary)

            HStack(spacing: 10) {
                Button(action: {
                    isFollowing.toggle()
                    HapticManager.shared.trigger(.success)
                    ToastManager.shared.show(isFollowing ? "Following @\(user.username)" : "Unfollowed", style: .info)
                }) {
                    Text(isFollowing ? "Following" : "Follow")
                        .font(.system(size: 12, weight: .bold))
                        .foregroundColor(isFollowing ? UsColors.textPrimary : .black)
                        .padding(.horizontal, 16)
                        .padding(.vertical, 8)
                        .background(isFollowing ? UsColors.bgTertiary : Color.white)
                        .clipShape(Capsule())
                }

                Button(action: {
                    isShowingPreview = false
                    onProfileTapped(user)
                }) {
                    Text("View Profile")
                        .font(.system(size: 12, weight: .bold))
                        .foregroundColor(UsColors.textPrimary)
                        .padding(.horizontal, 16)
                        .padding(.vertical, 8)
                        .background(UsColors.bgTertiary)
                        .clipShape(Capsule())
                }
            }
        }
        .padding(18)
        .frame(width: 220)
        .background(UsColors.bgSecondary)
    }
}
