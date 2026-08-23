import SwiftUI
import UsModel
import UsDesignSystem

public struct LiveLocationView: View {
    public let recipientName: String
    public let onDismiss: () -> Void

    @State private var isSharing: Bool = false
    @State private var selectedDurationMins: Int = 60

    private let durations = [15, 60, 480]

    public init(
        recipientName: String = "Sarah",
        onDismiss: @escaping () -> Void = {}
    ) {
        self.recipientName = recipientName
        self.onDismiss = onDismiss
    }

    public var body: some View {
        NavigationStack {
            ZStack {
                UsColors.bgPrimary
                    .ignoresSafeArea()

                VStack(spacing: 24) {
                    // Map Simulation View
                    ZStack {
                        Rectangle()
                            .fill(Color(red: 0x14/255.0, green: 0x1E/255.0, blue: 0x2A/255.0))

                        VStack(spacing: 12) {
                            ZStack {
                                Circle()
                                    .fill(UsColors.postbookPrimary.opacity(0.2))
                                    .frame(width: 80, height: 80)
                                Circle()
                                    .fill(UsColors.postbookPrimary)
                                    .frame(width: 24, height: 24)
                                Circle()
                                    .stroke(Color.white, lineWidth: 2)
                                    .frame(width: 24, height: 24)
                            }

                            Text("Koramangala 4th Block, Bengaluru")
                                .font(.system(size: 13, weight: .semibold))
                                .foregroundColor(.white)
                        }
                    }
                    .frame(height: 220)
                    .clipShape(RoundedRectangle(cornerRadius: 18))
                    .padding(.horizontal, 16)

                    if isSharing {
                        // Active sharing banner
                        VStack(spacing: 8) {
                            HStack(spacing: 6) {
                                Circle().fill(UsColors.onlineGreen).frame(width: 8, height: 8)
                                Text("Sharing live location with \(recipientName)")
                                    .font(.system(size: 14, weight: .bold))
                                    .foregroundColor(UsColors.textPrimary)
                            }

                            Text("Ends in 58 minutes • Accurate to 5 meters")
                                .font(.system(size: 12))
                                .foregroundColor(UsColors.textMuted)
                        }
                        .padding(16)
                        .frame(maxWidth: .infinity)
                        .background(UsColors.bgSecondary)
                        .clipShape(RoundedRectangle(cornerRadius: 14))
                        .padding(.horizontal, 16)

                        Button(action: {
                            isSharing = false
                            HapticManager.shared.trigger(.light)
                            ToastManager.shared.show("Stopped sharing live location", style: .info)
                        }) {
                            Text("Stop Sharing")
                                .font(.system(size: 15, weight: .bold))
                                .foregroundColor(UsColors.liveRed)
                                .padding(.horizontal, 24)
                                .padding(.vertical, 12)
                                .background(UsColors.liveRed.opacity(0.15))
                                .clipShape(Capsule())
                        }
                    } else {
                        // Duration selection
                        VStack(alignment: .leading, spacing: 12) {
                            Text("Share Live Location For:")
                                .font(.system(size: 14, weight: .semibold))
                                .foregroundColor(UsColors.textPrimary)

                            HStack(spacing: 12) {
                                ForEach(durations, id: \.self) { duration in
                                    let isSelected = selectedDurationMins == duration
                                    Button(action: {
                                        selectedDurationMins = duration
                                        HapticManager.shared.trigger(.selection)
                                    }) {
                                        Text(duration >= 60 ? "\(duration / 60) \(duration == 60 ? "Hour" : "Hours")" : "\(duration) Mins")
                                            .font(.system(size: 13, weight: .bold))
                                            .foregroundColor(isSelected ? .black : UsColors.textPrimary)
                                            .padding(.horizontal, 16)
                                            .padding(.vertical, 10)
                                            .background(isSelected ? Color.white : UsColors.bgSecondary)
                                            .clipShape(Capsule())
                                    }
                                    .buttonStyle(.plain)
                                }
                            }
                        }
                        .padding(.horizontal, 16)

                        Button(action: {
                            isSharing = true
                            HapticManager.shared.trigger(.success)
                            ToastManager.shared.show("Live location shared with \(recipientName) for \(selectedDurationMins) mins", style: .success)
                        }) {
                            HStack {
                                Spacer()
                                Text("Start Sharing Live Location")
                                    .font(.system(size: 15, weight: .bold))
                                    .foregroundColor(.black)
                                Spacer()
                            }
                            .padding(.vertical, 16)
                            .background(Color.white)
                            .clipShape(RoundedRectangle(cornerRadius: 14))
                        }
                        .padding(16)
                    }

                    Spacer()
                }
                .padding(.top, 12)
            }
            .navigationTitle("Live Location")
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
