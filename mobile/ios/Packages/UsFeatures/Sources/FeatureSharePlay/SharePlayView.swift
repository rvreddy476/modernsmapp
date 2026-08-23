import SwiftUI
import UsModel
import UsDesignSystem

public struct SharePlayView: View {
    public let mediaTitle: String
    public let onDismiss: () -> Void

    @State private var isSharePlayActive: Bool = true
    @State private var connectedMembers: [String] = ["You (Host)", "Sarah Chen", "Marcus Vance"]

    public init(
        mediaTitle: String = "Building India's #1 Super-App in 2026 🇮🇳",
        onDismiss: @escaping () -> Void = {}
    ) {
        self.mediaTitle = mediaTitle
        self.onDismiss = onDismiss
    }

    public var body: some View {
        NavigationStack {
            ZStack {
                UsColors.bgPrimary
                    .ignoresSafeArea()

                VStack(spacing: 24) {
                    // SharePlay Active Banner
                    HStack(spacing: 12) {
                        ZStack {
                            Circle().fill(Color.green.opacity(0.2)).frame(width: 44, height: 44)
                            Image(systemName: "shareplay")
                                .foregroundColor(Color.green)
                                .font(.system(size: 22))
                        }

                        VStack(alignment: .leading, spacing: 2) {
                            Text("Apple SharePlay Active 🟢")
                                .font(.system(size: 15, weight: .bold))
                                .foregroundColor(UsColors.textPrimary)
                            Text("Synced 4K video playback across all devices")
                                .font(.system(size: 11))
                                .foregroundColor(UsColors.textMuted)
                        }

                        Spacer()
                    }
                    .padding(14)
                    .background(UsColors.bgSecondary)
                    .clipShape(RoundedRectangle(cornerRadius: 16))

                    // Connected FaceTime Members
                    VStack(alignment: .leading, spacing: 12) {
                        Text("FaceTime Participants (\(connectedMembers.count))")
                            .font(.system(size: 14, weight: .bold))
                            .foregroundColor(UsColors.textPrimary)

                        ForEach(connectedMembers, id: \.self) { member in
                            HStack(spacing: 10) {
                                UsAvatar(name: member, size: .small)
                                Text(member)
                                    .font(.system(size: 13, weight: .semibold))
                                    .foregroundColor(UsColors.textPrimary)

                                Spacer()

                                Image(systemName: "checkmark.circle.fill")
                                    .foregroundColor(UsColors.onlineGreen)
                                    .font(.system(size: 14))
                            }
                            .padding(10)
                            .background(UsColors.bgTertiary)
                            .clipShape(RoundedRectangle(cornerRadius: 10))
                        }
                    }
                    .padding(16)
                    .frame(maxWidth: .infinity, alignment: .leading)
                    .background(UsColors.bgSecondary)
                    .clipShape(RoundedRectangle(cornerRadius: 16))

                    Spacer()

                    Button(action: {
                        HapticManager.shared.trigger(.success)
                        ToastManager.shared.show("SharePlay Session ended", style: .info)
                        onDismiss()
                    }) {
                        Text("End SharePlay Session")
                            .font(.system(size: 14, weight: .bold))
                            .foregroundColor(UsColors.liveRed)
                            .padding(.vertical, 14)
                            .frame(maxWidth: .infinity)
                            .background(UsColors.liveRed.opacity(0.15))
                            .clipShape(RoundedRectangle(cornerRadius: 14))
                    }
                    .padding(.horizontal, 16)
                }
                .padding(16)
            }
            .navigationTitle("SharePlay Co-Viewing")
            .navigationBarTitleDisplayMode(.inline)
            .toolbar {
                ToolbarItem(placement: .cancellationAction) {
                    Button("Close", action: onDismiss)
                        .foregroundColor(UsColors.textMuted)
                }
            }
        }
    }
}
