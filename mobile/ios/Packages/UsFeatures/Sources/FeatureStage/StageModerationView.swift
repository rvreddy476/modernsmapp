import SwiftUI
import UsModel
import UsDesignSystem
import UsNetwork

public struct StageHandRaiseItem: Identifiable {
    public let id: String
    public let user: Author
    public let requestedAgo: String

    public init(id: String, user: Author, requestedAgo: String = "2m ago") {
        self.id = id
        self.user = user
        self.requestedAgo = requestedAgo
    }
}

public struct StageModerationView: View {
    public let onDismiss: () -> Void

    @State private var handRaises: [StageHandRaiseItem] = [
        StageHandRaiseItem(id: "hr-1", user: Author(id: "u1", username: "marcus_v", displayName: "Marcus Vance"), requestedAgo: "Just now"),
        StageHandRaiseItem(id: "hr-2", user: Author(id: "u2", username: "aanya_s", displayName: "Aanya Sharma"), requestedAgo: "1m ago"),
        StageHandRaiseItem(id: "hr-3", user: Author(id: "u3", username: "dev_p", displayName: "Dev Patel"), requestedAgo: "3m ago")
    ]

    public init(onDismiss: @escaping () -> Void = {}) {
        self.onDismiss = onDismiss
    }

    public var body: some View {
        NavigationStack {
            ZStack {
                UsColors.bgPrimary
                    .ignoresSafeArea()

                VStack(spacing: 18) {
                    // Stage Quick Controls
                    HStack(spacing: 12) {
                        Button(action: {
                            HapticManager.shared.trigger(.medium)
                            ToastManager.shared.show("All speakers muted", style: .info)
                        }) {
                            HStack(spacing: 6) {
                                Image(systemName: "mic.slash.fill")
                                Text("Mute All")
                            }
                            .font(.system(size: 13, weight: .bold))
                            .foregroundColor(UsColors.liveRed)
                            .padding(.horizontal, 16)
                            .padding(.vertical, 10)
                            .background(UsColors.liveRed.opacity(0.15))
                            .clipShape(Capsule())
                        }

                        Spacer()

                        Text("\(handRaises.count) Raised Hands")
                            .font(.system(size: 13, weight: .semibold))
                            .foregroundColor(UsColors.textMuted)
                    }
                    .padding(.horizontal, 16)
                    .padding(.top, 8)

                    // Hand Raises List
                    if handRaises.isEmpty {
                        VStack(spacing: 8) {
                            Spacer()
                            Image(systemName: "hand.raised.slash.fill")
                                .font(.system(size: 40))
                                .foregroundColor(UsColors.textDim)
                            Text("No pending speaker requests")
                                .font(.system(size: 14))
                                .foregroundColor(UsColors.textMuted)
                            Spacer()
                        }
                    } else {
                        ScrollView {
                            LazyVStack(spacing: 12) {
                                ForEach(handRaises) { item in
                                    handRaiseRow(item)
                                }
                            }
                            .padding(.horizontal, 16)
                        }
                    }
                }
            }
            .navigationTitle("Stage Requests")
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
    private func handRaiseRow(_ item: StageHandRaiseItem) -> some View {
        HStack(spacing: 12) {
            UsAvatar(name: item.user.nameForDisplay, url: item.user.avatarUrl, size: .medium)

            VStack(alignment: .leading, spacing: 2) {
                Text(item.user.nameForDisplay)
                    .font(.system(size: 14, weight: .bold))
                    .foregroundColor(UsColors.textPrimary)
                Text("@\(item.user.username) • \(item.requestedAgo)")
                    .font(.system(size: 12))
                    .foregroundColor(UsColors.textMuted)
            }

            Spacer()

            // Decline button
            Button(action: {
                handRaises.removeAll { $0.id == item.id }
                HapticManager.shared.trigger(.light)
            }) {
                Image(systemName: "xmark")
                    .font(.system(size: 13, weight: .bold))
                    .foregroundColor(UsColors.textMuted)
                    .padding(8)
                    .background(UsColors.bgTertiary)
                    .clipShape(Circle())
            }

            // Accept to Stage button
            Button(action: {
                handRaises.removeAll { $0.id == item.id }
                HapticManager.shared.trigger(.success)
                ToastManager.shared.show("\(item.user.nameForDisplay) invited to speak on stage! 🎙️", style: .success)
            }) {
                Text("Invite to Speak")
                    .font(.system(size: 12, weight: .bold))
                    .foregroundColor(.black)
                    .padding(.horizontal, 14)
                    .padding(.vertical, 8)
                    .background(Color.white)
                    .clipShape(Capsule())
            }
        }
        .padding(14)
        .background(UsColors.bgSecondary)
        .clipShape(RoundedRectangle(cornerRadius: 16))
    }
}
