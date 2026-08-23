import SwiftUI
import UsModel
import UsDesignSystem

public struct E2EESafetyNumberView: View {
    public let contact: Author
    public let onDismiss: () -> Void

    @State private var isVerified: Bool = false
    private let safetyNumberChunks: [String] = [
        "28491", "94820", "57291", "10482",
        "73910", "48201", "84920", "39102",
        "58291", "04829", "19482", "67291"
    ]

    public init(
        contact: Author = Author(id: "c1", username: "sarah_c", displayName: "Sarah Chen"),
        onDismiss: @escaping () -> Void = {}
    ) {
        self.contact = contact
        self.onDismiss = onDismiss
    }

    public var body: some View {
        NavigationStack {
            ZStack {
                UsColors.bgPrimary
                    .ignoresSafeArea()

                ScrollView {
                    VStack(spacing: 20) {
                        // Header
                        VStack(spacing: 6) {
                            Image(systemName: "lock.shield.fill")
                                .font(.system(size: 40))
                                .foregroundColor(UsColors.onlineGreen)

                            Text("Verify End-to-End Encryption")
                                .font(.system(size: 18, weight: .bold))
                                .foregroundColor(UsColors.textPrimary)

                            Text("Compare this safety number with \(contact.nameForDisplay) to verify that your messages and calls are encrypted end-to-end.")
                                .font(.system(size: 12))
                                .foregroundColor(UsColors.textMuted)
                                .multilineTextAlignment(.center)
                                .padding(.horizontal, 16)
                        }
                        .padding(.top, 12)

                        // 60-Digit Numbers Grid
                        LazyVGrid(columns: [GridItem(.flexible()), GridItem(.flexible()), GridItem(.flexible()), GridItem(.flexible())], spacing: 10) {
                            ForEach(safetyNumberChunks, id: \.self) { chunk in
                                Text(chunk)
                                    .font(.system(size: 14, weight: .bold, design: .monospaced))
                                    .foregroundColor(UsColors.textPrimary)
                                    .padding(.vertical, 6)
                                    .padding(.horizontal, 8)
                                    .background(UsColors.bgSecondary)
                                    .clipShape(RoundedRectangle(cornerRadius: 8))
                            }
                        }
                        .padding(16)
                        .background(UsColors.bgTertiary)
                        .clipShape(RoundedRectangle(cornerRadius: 16))
                        .padding(.horizontal, 16)

                        // Verification toggle button
                        Button(action: {
                            isVerified.toggle()
                            HapticManager.shared.trigger(.success)
                            ToastManager.shared.show(isVerified ? "Safety numbers verified with \(contact.nameForDisplay)!" : "Verification reset", style: .success)
                        }) {
                            HStack(spacing: 8) {
                                Image(systemName: isVerified ? "checkmark.circle.fill" : "circle")
                                Text(isVerified ? "Verified with Contact" : "Mark as Verified")
                            }
                            .font(.system(size: 14, weight: .bold))
                            .foregroundColor(isVerified ? .black : UsColors.textPrimary)
                            .padding(.horizontal, 24)
                            .padding(.vertical, 12)
                            .background(isVerified ? UsColors.onlineGreen : UsColors.bgSecondary)
                            .clipShape(Capsule())
                        }
                    }
                    .padding(16)
                }
            }
            .navigationTitle("Encryption Keys")
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
