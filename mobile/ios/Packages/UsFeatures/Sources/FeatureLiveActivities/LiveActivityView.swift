import SwiftUI
import UsModel
import UsDesignSystem

public enum DeliveryStage: String, CaseIterable {
    case preparing = "Preparing 👨‍🍳"
    case onTheWay = "Out for Delivery 🛵"
    case arriving = "Arriving in 2 mins 📍"
    case delivered = "Delivered 🎉"
}

public struct LiveActivityView: View {
    public let orderTitle: String
    public let restaurantName: String
    public let etaMinutes: Int
    public let stage: DeliveryStage
    public let onDismiss: () -> Void

    @State private var currentStage: DeliveryStage

    public init(
        orderTitle: String = "Kadai Paneer & 3x Butter Naan",
        restaurantName: String = "Punjab Grill Indiranagar",
        etaMinutes: Int = 14,
        stage: DeliveryStage = .onTheWay,
        onDismiss: @escaping () -> Void = {}
    ) {
        self.orderTitle = orderTitle
        self.restaurantName = restaurantName
        self.etaMinutes = etaMinutes
        self.stage = stage
        self.onDismiss = onDismiss
        self._currentStage = State(initialValue: stage)
    }

    public var body: some View {
        NavigationStack {
            ZStack {
                UsColors.bgPrimary
                    .ignoresSafeArea()

                ScrollView {
                    VStack(alignment: .leading, spacing: 24) {
                        Text("Dynamic Island & Lock Screen Live Activity")
                            .font(.system(size: 13))
                            .foregroundColor(UsColors.textMuted)

                        // Lock Screen Widget Preview
                        VStack(alignment: .leading, spacing: 14) {
                            HStack {
                                HStack(spacing: 8) {
                                    Image(systemName: "fork.knife.circle.fill")
                                        .font(.system(size: 22))
                                        .foregroundColor(UsColors.postbookPrimary)

                                    VStack(alignment: .leading, spacing: 1) {
                                        Text(restaurantName)
                                            .font(.system(size: 14, weight: .bold))
                                            .foregroundColor(.white)
                                        Text(orderTitle)
                                            .font(.system(size: 11))
                                            .foregroundColor(.white.opacity(0.7))
                                    }
                                }

                                Spacer()

                                VStack(alignment: .trailing, spacing: 2) {
                                    Text("\(etaMinutes) MINS")
                                        .font(.system(size: 16, weight: .black, design: .rounded))
                                        .foregroundColor(UsColors.onlineGreen)
                                    Text("Estimated Time")
                                        .font(.system(size: 9, weight: .semibold))
                                        .foregroundColor(.white.opacity(0.6))
                                }
                            }

                            // Progress track
                            HStack(spacing: 6) {
                                ForEach(DeliveryStage.allCases, id: \.self) { s in
                                    let isDone = isStageCompleted(s)
                                    Capsule()
                                        .fill(isDone ? UsColors.onlineGreen : Color.white.opacity(0.2))
                                        .frame(height: 5)
                                }
                            }

                            HStack {
                                Text(currentStage.rawValue)
                                    .font(.system(size: 12, weight: .bold))
                                    .foregroundColor(UsColors.onlineGreen)

                                Spacer()

                                Text("Rider: Suresh Kumar (Hero Splendor)")
                                    .font(.system(size: 10))
                                    .foregroundColor(.white.opacity(0.6))
                            }
                        }
                        .padding(18)
                        .background(Color.black.opacity(0.9))
                        .clipShape(RoundedRectangle(cornerRadius: 22))
                        .overlay(RoundedRectangle(cornerRadius: 22).stroke(Color.white.opacity(0.2), lineWidth: 1))
                        .shadow(color: Color.black.opacity(0.4), radius: 12, x: 0, y: 6)

                        // Dynamic Island Expanded Mode Preview
                        VStack(spacing: 12) {
                            Text("Dynamic Island Expanded Preview")
                                .font(.system(size: 12, weight: .bold))
                                .foregroundColor(UsColors.textPrimary)

                            HStack {
                                Image(systemName: "moped.fill")
                                    .font(.system(size: 18))
                                    .foregroundColor(UsColors.onlineGreen)

                                VStack(alignment: .leading, spacing: 1) {
                                    Text("Punjab Grill")
                                        .font(.system(size: 12, weight: .bold))
                                        .foregroundColor(.white)
                                    Text("Rider 0.8 km away")
                                        .font(.system(size: 10))
                                        .foregroundColor(.white.opacity(0.7))
                                }

                                Spacer()

                                Text("\(etaMinutes)m")
                                    .font(.system(size: 14, weight: .black, design: .rounded))
                                    .foregroundColor(UsColors.onlineGreen)
                            }
                            .padding(.horizontal, 16)
                            .padding(.vertical, 12)
                            .background(Color.black)
                            .clipShape(Capsule())
                            .overlay(Capsule().stroke(Color.white.opacity(0.15), lineWidth: 1))
                        }
                        .padding(16)
                        .frame(maxWidth: .infinity)
                        .background(UsColors.bgSecondary)
                        .clipShape(RoundedRectangle(cornerRadius: 18))
                    }
                    .padding(16)
                }
            }
            .navigationTitle("Live Activities")
            .navigationBarTitleDisplayMode(.inline)
            .toolbar {
                ToolbarItem(placement: .cancellationAction) {
                    Button("Close", action: onDismiss)
                        .foregroundColor(UsColors.textMuted)
                }
            }
        }
    }

    private func isStageCompleted(_ checkStage: DeliveryStage) -> Bool {
        let all = DeliveryStage.allCases
        guard let currentIdx = all.firstIndex(of: currentStage),
              let checkIdx = all.firstIndex(of: checkStage) else { return false }
        return checkIdx <= currentIdx
    }
}
