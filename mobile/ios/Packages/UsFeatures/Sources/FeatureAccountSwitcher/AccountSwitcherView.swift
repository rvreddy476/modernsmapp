import SwiftUI
import UsModel
import UsDesignSystem
import UsNetwork

public struct UserAccountProfile: Identifiable, Hashable {
    public let id: String
    public let username: String
    public let displayName: String
    public let typeDescription: String
    public let avatarUrl: String?
    public let unreadCount: Int

    public init(
        id: String,
        username: String,
        displayName: String,
        typeDescription: String = "Personal Account",
        avatarUrl: String? = nil,
        unreadCount: Int = 0
    ) {
        self.id = id
        self.username = username
        self.displayName = displayName
        self.typeDescription = typeDescription
        self.avatarUrl = avatarUrl
        self.unreadCount = unreadCount
    }
}

public struct AccountSwitcherView: View {
    public let onSelectAccount: (UserAccountProfile) -> Void
    public let onDismiss: () -> Void

    @State private var accounts: [UserAccountProfile] = [
        UserAccountProfile(id: "acc-1", username: "alex", displayName: "Alex Rivera", typeDescription: "Personal Account", unreadCount: 3),
        UserAccountProfile(id: "acc-2", username: "alex_designs", displayName: "Alex Rivera Studio", typeDescription: "Creator Page 🎨", unreadCount: 12),
        UserAccountProfile(id: "acc-3", username: "urban_roasters", displayName: "Urban Roasters Coffee", typeDescription: "Merchant Business ☕️", unreadCount: 0)
    ]
    @State private var activeAccountId: String = "acc-1"

    public init(
        onSelectAccount: @escaping (UserAccountProfile) -> Void = { _ in },
        onDismiss: @escaping () -> Void = {}
    ) {
        self.onSelectAccount = onSelectAccount
        self.onDismiss = onDismiss
    }

    public var body: some View {
        NavigationStack {
            ZStack {
                UsColors.bgPrimary
                    .ignoresSafeArea()

                VStack(spacing: 16) {
                    VStack(spacing: 10) {
                        ForEach(accounts) { acc in
                            accountRow(acc)
                        }
                    }

                    // Add new account button
                    Button(action: {
                        ToastManager.shared.show("Log into another US account", style: .info)
                    }) {
                        HStack(spacing: 12) {
                            ZStack {
                                Circle().fill(UsColors.bgTertiary).frame(width: 44, height: 44)
                                Image(systemName: "plus")
                                    .font(.system(size: 16, weight: .bold))
                                    .foregroundColor(UsColors.textPrimary)
                            }

                            Text("Add Another Account")
                                .font(.system(size: 15, weight: .semibold))
                                .foregroundColor(UsColors.textPrimary)

                            Spacer()
                        }
                        .padding(12)
                        .background(UsColors.bgSecondary)
                        .clipShape(RoundedRectangle(cornerRadius: 14))
                    }
                    .buttonStyle(.plain)

                    Spacer()
                }
                .padding(16)
            }
            .navigationTitle("Switch Account")
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
    private func accountRow(_ acc: UserAccountProfile) -> some View {
        let isActive = activeAccountId == acc.id
        Button(action: {
            activeAccountId = acc.id
            HapticManager.shared.trigger(.selection)
            onSelectAccount(acc)
            ToastManager.shared.show("Switched to @\(acc.username)", style: .success)
            onDismiss()
        }) {
            HStack(spacing: 14) {
                UsAvatar(name: acc.displayName, url: acc.avatarUrl, size: .medium)

                VStack(alignment: .leading, spacing: 2) {
                    Text(acc.displayName)
                        .font(.system(size: 15, weight: .bold))
                        .foregroundColor(UsColors.textPrimary)

                    Text("@\(acc.username) • \(acc.typeDescription)")
                        .font(.system(size: 12))
                        .foregroundColor(UsColors.textMuted)
                }

                Spacer()

                if acc.unreadCount > 0 {
                    Text("\(acc.unreadCount)")
                        .font(.system(size: 11, weight: .bold))
                        .foregroundColor(.white)
                        .padding(.horizontal, 8)
                        .padding(.vertical, 3)
                        .background(UsColors.liveRed)
                        .clipShape(Capsule())
                }

                if isActive {
                    Image(systemName: "checkmark.circle.fill")
                        .font(.system(size: 20))
                        .foregroundColor(UsColors.postbookPrimary)
                }
            }
            .padding(14)
            .background(UsColors.bgSecondary)
            .clipShape(RoundedRectangle(cornerRadius: 16))
            .overlay(
                RoundedRectangle(cornerRadius: 16)
                    .stroke(isActive ? UsColors.postbookPrimary : UsColors.borderSubtle, lineWidth: isActive ? 1.5 : 1)
            )
        }
        .buttonStyle(.plain)
    }
}
