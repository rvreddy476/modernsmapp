import SwiftUI
import UsModel
import UsDesignSystem

public struct WatchSyncView: View {
    public let onDismiss: () -> Void

    @State private var isWristUPIEnabled: Bool = true
    @State private var isHealthKitSyncEnabled: Bool = true
    @State private var isHapticAlertsEnabled: Bool = true
    @State private var syncedStepsToday: Int = 8420

    public init(onDismiss: @escaping () -> Void = {}) {
        self.onDismiss = onDismiss
    }

    public var body: some View {
        NavigationStack {
            ZStack {
                UsColors.bgPrimary
                    .ignoresSafeArea()

                ScrollView {
                    VStack(alignment: .leading, spacing: 20) {
                        // Watch Device Banner
                        HStack(spacing: 14) {
                            ZStack {
                                Circle().fill(Color.orange.opacity(0.2)).frame(width: 48, height: 48)
                                Image(systemName: "applewatch")
                                    .foregroundColor(Color.orange)
                                    .font(.system(size: 24))
                            }

                            VStack(alignment: .leading, spacing: 2) {
                                Text("Apple Watch Series 9 (Synced 🟢)")
                                    .font(.system(size: 15, weight: .bold))
                                    .foregroundColor(UsColors.textPrimary)
                                Text("Wrist-ready UPI, HealthKit streaks & Voice DMs")
                                    .font(.system(size: 11))
                                    .foregroundColor(UsColors.textMuted)
                            }
                        }
                        .padding(14)
                        .frame(maxWidth: .infinity, alignment: .leading)
                        .background(UsColors.bgSecondary)
                        .clipShape(RoundedRectangle(cornerRadius: 16))

                        // HealthKit Fitness Streak Card
                        VStack(alignment: .leading, spacing: 8) {
                            Text("Fitness Rewards Sync (HealthKit)")
                                .font(.system(size: 13, weight: .bold))
                                .foregroundColor(UsColors.textPrimary)

                            HStack {
                                VStack(alignment: .leading, spacing: 2) {
                                    Text("\(syncedStepsToday) Steps")
                                        .font(.system(size: 20, weight: .black, design: .rounded))
                                        .foregroundColor(UsColors.onlineGreen)
                                    Text("Goal: 10,000 steps • 84% completed")
                                        .font(.system(size: 11))
                                        .foregroundColor(UsColors.textMuted)
                                }

                                Spacer()

                                Image(systemName: "flame.fill")
                                    .font(.system(size: 28))
                                    .foregroundColor(Color.orange)
                            }
                            .padding(14)
                            .background(UsColors.bgSecondary)
                            .clipShape(RoundedRectangle(cornerRadius: 14))
                        }

                        Text("Wrist Preferences")
                            .font(.system(size: 15, weight: .bold))
                            .foregroundColor(UsColors.textPrimary)

                        VStack(spacing: 12) {
                            Toggle("1-Tap Wrist UPI Payments", isOn: $isWristUPIEnabled)
                                .tint(UsColors.postbookPrimary)
                            Divider().background(UsColors.borderSubtle)

                            Toggle("Sync Apple Health Calories & Steps", isOn: $isHealthKitSyncEnabled)
                                .tint(UsColors.postbookPrimary)
                            Divider().background(UsColors.borderSubtle)

                            Toggle("Wrist Haptic Alerts for DMs & Tips", isOn: $isHapticAlertsEnabled)
                                .tint(UsColors.postbookPrimary)
                        }
                        .padding(16)
                        .background(UsColors.bgSecondary)
                        .clipShape(RoundedRectangle(cornerRadius: 16))
                    }
                    .padding(16)
                }
            }
            .navigationTitle("Apple Watch")
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
