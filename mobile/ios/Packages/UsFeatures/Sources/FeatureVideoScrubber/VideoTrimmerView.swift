import SwiftUI
import UsModel
import UsDesignSystem

public struct VideoTrimmerView: View {
    public let maxDurationSeconds: Double
    public let onTrimChanged: (Double, Double) -> Void
    public let onDismiss: () -> Void

    @State private var startTrim: Double = 0.0 // 0.0 to 1.0
    @State private var endTrim: Double = 1.0 // 0.0 to 1.0
    @State private var currentPlayback: Double = 0.3

    public init(
        maxDurationSeconds: Double = 60.0,
        onTrimChanged: @escaping (Double, Double) -> Void = { _, _ in },
        onDismiss: @escaping () -> Void = {}
    ) {
        self.maxDurationSeconds = maxDurationSeconds
        self.onTrimChanged = onTrimChanged
        self.onDismiss = onDismiss
    }

    private var trimmedDurationString: String {
        let trimmedSeconds = (endTrim - startTrim) * maxDurationSeconds
        return String(format: "%.1fs", trimmedSeconds)
    }

    public var body: some View {
        NavigationStack {
            ZStack {
                Color.black.ignoresSafeArea()

                VStack(spacing: 24) {
                    // Video Preview Frame Placeholder
                    ZStack {
                        Rectangle()
                            .fill(Color(red: 0x14/255.0, green: 0x14/255.0, blue: 0x22/255.0))

                        VStack(spacing: 8) {
                            Image(systemName: "video.fill")
                                .font(.system(size: 44))
                                .foregroundColor(UsColors.postbookPrimary)
                            Text("Trim Video: \(trimmedDurationString)")
                                .font(.system(size: 15, weight: .bold))
                                .foregroundColor(.white)
                        }
                    }
                    .frame(height: 320)
                    .clipShape(RoundedRectangle(cornerRadius: 16))
                    .padding(.horizontal, 16)

                    // Trimmer Timeline Strip
                    VStack(alignment: .leading, spacing: 8) {
                        HStack {
                            Text("Timeline Strip")
                                .font(.system(size: 13, weight: .semibold))
                                .foregroundColor(.white.opacity(0.8))

                            Spacer()

                            Text(trimmedDurationString)
                                .font(.system(size: 13, weight: .bold, design: .monospaced))
                                .foregroundColor(UsColors.postbookPrimary)
                        }
                        .padding(.horizontal, 16)

                        // Timeline frame strip
                        ZStack(alignment: .leading) {
                            // Filmstrip thumbnail bars
                            HStack(spacing: 2) {
                                ForEach(0..<14, id: \.self) { _ in
                                    Rectangle()
                                        .fill(Color(red: 0x28/255.0, green: 0x28/255.0, blue: 0x38/255.0))
                                        .frame(height: 54)
                                }
                            }
                            .clipShape(RoundedRectangle(cornerRadius: 8))

                            // Trim overlay box with handles
                            GeometryReader { geo in
                                let leftOffset = geo.size.width * CGFloat(startTrim)
                                let width = max(40, geo.size.width * CGFloat(endTrim - startTrim))

                                ZStack(alignment: .leading) {
                                    // Highlighted active region
                                    RoundedRectangle(cornerRadius: 6)
                                        .stroke(Color.yellow, lineWidth: 3)
                                        .background(Color.yellow.opacity(0.15))
                                        .frame(width: width, height: 54)
                                        .offset(x: leftOffset)

                                    // Left Handle
                                    Circle()
                                        .fill(Color.yellow)
                                        .frame(width: 18, height: 18)
                                        .offset(x: leftOffset - 9, y: 18)
                                        .gesture(
                                            DragGesture()
                                                .onChanged { val in
                                                    let newStart = max(0.0, min(endTrim - 0.1, Double(val.location.x / geo.size.width)))
                                                    startTrim = newStart
                                                    HapticManager.shared.trigger(.selection)
                                                }
                                        )

                                    // Right Handle
                                    Circle()
                                        .fill(Color.yellow)
                                        .frame(width: 18, height: 18)
                                        .offset(x: leftOffset + width - 9, y: 18)
                                        .gesture(
                                            DragGesture()
                                                .onChanged { val in
                                                    let newEnd = min(1.0, max(startTrim + 0.1, Double(val.location.x / geo.size.width)))
                                                    endTrim = newEnd
                                                    HapticManager.shared.trigger(.selection)
                                                }
                                        )
                                }
                            }
                        }
                        .frame(height: 54)
                        .padding(.horizontal, 16)
                    }

                    Spacer()

                    // Action Buttons
                    HStack(spacing: 16) {
                        Button(action: onDismiss) {
                            Text("Reset")
                                .font(.system(size: 14, weight: .semibold))
                                .foregroundColor(.white)
                                .padding(.horizontal, 20)
                                .padding(.vertical, 12)
                                .background(Color.white.opacity(0.2))
                                .clipShape(Capsule())
                        }

                        Spacer()

                        Button(action: {
                            HapticManager.shared.trigger(.success)
                            onTrimChanged(startTrim * maxDurationSeconds, endTrim * maxDurationSeconds)
                            ToastManager.shared.show("Video Trimmed to \(trimmedDurationString)", style: .success)
                            onDismiss()
                        }) {
                            Text("Apply Trim")
                                .font(.system(size: 15, weight: .bold))
                                .foregroundColor(.black)
                                .padding(.horizontal, 24)
                                .padding(.vertical, 12)
                                .background(Color.white)
                                .clipShape(Capsule())
                        }
                    }
                    .padding(16)
                }
            }
            .navigationTitle("Trim Video")
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
