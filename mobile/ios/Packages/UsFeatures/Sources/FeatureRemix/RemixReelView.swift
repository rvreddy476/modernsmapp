import SwiftUI
import UsModel
import UsDesignSystem

public struct RemixReelView: View {
    public let originalAuthor: Author
    public let onPublishRemix: () -> Void
    public let onDismiss: () -> Void

    @State private var isRecording: Bool = false
    @State private var recordedSeconds: Double = 0.0

    public init(
        originalAuthor: Author = Author(id: "c1", username: "sarah_c", displayName: "Sarah Chen"),
        onPublishRemix: @escaping () -> Void = {},
        onDismiss: @escaping () -> Void = {}
    ) {
        self.originalAuthor = originalAuthor
        self.onPublishRemix = onPublishRemix
        self.onDismiss = onDismiss
    }

    public var body: some View {
        ZStack {
            Color.black.ignoresSafeArea()

            VStack(spacing: 0) {
                // Dual Side-by-Side View
                HStack(spacing: 4) {
                    // Left: Original Reel Video
                    ZStack(alignment: .bottomLeading) {
                        Rectangle()
                            .fill(Color(red: 0x18/255.0, green: 0x1E/255.0, blue: 0x2A/255.0))

                        VStack(alignment: .leading, spacing: 4) {
                            Text("Original by")
                                .font(.system(size: 10))
                                .foregroundColor(.white.opacity(0.7))
                            Text("@\(originalAuthor.username)")
                                .font(.system(size: 12, weight: .bold))
                                .foregroundColor(.white)
                        }
                        .padding(10)
                    }

                    // Right: Live Camera Feed for Creator
                    ZStack(alignment: .topTrailing) {
                        Rectangle()
                            .fill(Color(red: 0x24/255.0, green: 0x14/255.0, blue: 0x22/255.0))

                        if isRecording {
                            Circle()
                                .fill(Color.red)
                                .frame(width: 12, height: 12)
                                .padding(10)
                        }
                    }
                }
                .clipShape(RoundedRectangle(cornerRadius: 18))
                .padding(.horizontal, 8)
                .padding(.top, 44)
                .padding(.bottom, 100)
            }

            // Top Header & Exit
            VStack {
                HStack {
                    Button(action: onDismiss) {
                        Image(systemName: "xmark")
                            .font(.system(size: 18, weight: .bold))
                            .foregroundColor(.white)
                            .padding(10)
                            .background(Color.black.opacity(0.6))
                            .clipShape(Circle())
                    }

                    Spacer()

                    Text("Remix with @\(originalAuthor.username)")
                        .font(.system(size: 14, weight: .bold))
                        .foregroundColor(.white)

                    Spacer()

                    Button(action: {
                        HapticManager.shared.trigger(.success)
                        ToastManager.shared.show("Remix Reel Published!", style: .success)
                        onPublishRemix()
                        onDismiss()
                    }) {
                        Text("Next")
                            .font(.system(size: 14, weight: .bold))
                            .foregroundColor(.black)
                            .padding(.horizontal, 16)
                            .padding(.vertical, 8)
                            .background(Color.white)
                            .clipShape(Capsule())
                    }
                }
                .padding(.horizontal, 16)
                .padding(.top, 8)

                Spacer()

                // Bottom Record Trigger
                Button(action: {
                    isRecording.toggle()
                    HapticManager.shared.trigger(.medium)
                }) {
                    ZStack {
                        Circle().stroke(Color.white, lineWidth: 4).frame(width: 74, height: 74)
                        Circle().fill(isRecording ? Color.red : Color.white).frame(width: 60, height: 60)
                    }
                }
                .padding(.bottom, 24)
            }
        }
    }
}
