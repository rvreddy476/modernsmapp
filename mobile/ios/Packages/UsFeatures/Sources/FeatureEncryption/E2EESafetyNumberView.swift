import SwiftUI
import UsModel
import UsDesignSystem

// P0-6 (chat production correction pass): this screen displayed "Verify
// End-to-End Encryption" with a hardcoded fake safety number and a toggle
// that "verified" nothing, over a plaintext backend. No end-to-end
// encryption exists in this product yet (see docs/adr/
// adr-chat-e2ee-implementation.md), and the directive forbids every E2EE
// claim — lock badges, safety numbers, "only you can read this" — until
// CH-LB-5 passes. The public API is preserved; the surface now states the
// truth and presents no verification theater.

public struct E2EESafetyNumberView: View {
    public let contact: Author
    public let onDismiss: () -> Void

    public init(
        contact: Author = Author(id: "", username: "", displayName: ""),
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

                VStack(spacing: 16) {
                    Image(systemName: "hourglass")
                        .font(.system(size: 40))
                        .foregroundColor(UsColors.textMuted)

                    Text("Encryption verification isn't available")
                        .font(.system(size: 18, weight: .bold))
                        .foregroundColor(UsColors.textPrimary)

                    Text("Messages in this app are protected in transit and at rest, but they are not end-to-end encrypted today, so there is no safety number to compare. This screen will exist only when there are real keys behind it.")
                        .font(.system(size: 13))
                        .foregroundColor(UsColors.textMuted)
                        .multilineTextAlignment(.center)
                        .padding(.horizontal, 24)
                }
            }
            .navigationTitle("Encryption")
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
