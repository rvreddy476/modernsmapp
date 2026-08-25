import SwiftUI
import UsModel
import UsDesignSystem
import UsNetwork

public struct CollabInviteView: View {
    public let onDismiss: () -> Void

    @State private var collaboratorHandle: String = "@sarah_c"
    @State private var creatorSharePercentage: Double = 50.0 // 50/50 default
    @State private var isSendingInvite: Bool = false

    public init(onDismiss: @escaping () -> Void = {}) {
        self.onDismiss = onDismiss
    }

    public var body: some View {
        NavigationStack {
            ZStack {
                UsColors.bgPrimary
                    .ignoresSafeArea()

                VStack(alignment: .leading, spacing: 20) {
                    Text("Tag a Co-Author & Split Monetization")
                        .font(.system(size: 14))
                        .foregroundColor(UsColors.textMuted)

                    // Collaborator Username Input
                    VStack(alignment: .leading, spacing: 6) {
                        Text("Collaborator Handle")
                            .font(.system(size: 13, weight: .semibold))
                            .foregroundColor(UsColors.textPrimary)

                        TextField("Enter @username", text: $collaboratorHandle)
                            .textFieldStyle(.plain)
                            .padding(14)
                            .background(UsColors.bgSecondary)
                            .clipShape(RoundedRectangle(cornerRadius: 12))
                            .foregroundColor(UsColors.textPrimary)
                    }

                    // Revenue Share Split Card
                    VStack(alignment: .leading, spacing: 14) {
                        Text("Automated Revenue Split")
                            .font(.system(size: 14, weight: .bold))
                            .foregroundColor(UsColors.textPrimary)

                        HStack {
                            VStack(alignment: .leading) {
                                Text("You (Creator)")
                                    .font(.system(size: 12))
                                    .foregroundColor(UsColors.textMuted)
                                Text(String(format: "%.0f%%", creatorSharePercentage))
                                    .font(.system(size: 22, weight: .bold, design: .rounded))
                                    .foregroundColor(UsColors.postbookPrimary)
                            }

                            Spacer()

                            VStack(alignment: .trailing) {
                                Text("Co-Author")
                                    .font(.system(size: 12))
                                    .foregroundColor(UsColors.textMuted)
                                Text(String(format: "%.0f%%", 100.0 - creatorSharePercentage))
                                    .font(.system(size: 22, weight: .bold, design: .rounded))
                                    .foregroundColor(UsColors.postgramPrimary)
                            }
                        }

                        Slider(value: $creatorSharePercentage, in: 10...90, step: 5)
                            .tint(UsColors.postbookPrimary)
                    }
                    .padding(16)
                    .background(UsColors.bgSecondary)
                    .clipShape(RoundedRectangle(cornerRadius: 16))

                    Spacer()

                    // Send Collab Invite
                    Button(action: sendInvite) {
                        HStack {
                            Spacer()
                            if isSendingInvite {
                                ProgressView().tint(.black)
                            } else {
                                Text("Send Collaboration Request")
                                    .font(.system(size: 15, weight: .bold))
                                    .foregroundColor(.black)
                            }
                            Spacer()
                        }
                        .padding(.vertical, 16)
                        .background(Color.white)
                        .clipShape(RoundedRectangle(cornerRadius: 14))
                    }
                    .disabled(collaboratorHandle.isEmpty || isSendingInvite)
                }
                .padding(16)
            }
            .navigationTitle("Invite Collaborator")
            .navigationBarTitleDisplayMode(.inline)
            .toolbar {
                ToolbarItem(placement: .cancellationAction) {
                    Button("Cancel", action: onDismiss)
                        .foregroundColor(UsColors.textMuted)
                }
            }
        }
    }

    private func sendInvite() {
        isSendingInvite = true
        DispatchQueue.main.asyncAfter(deadline: .now() + 1.0) {
            isSendingInvite = false
            HapticManager.shared.trigger(.success)
            ToastManager.shared.show("Collab invite sent to \(collaboratorHandle) with \(String(format: "%.0f", 100.0 - creatorSharePercentage))% revenue split!", style: .success)
            onDismiss()
        }
    }
}
