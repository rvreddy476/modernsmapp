import SwiftUI
import UsModel
import UsDesignSystem

public struct WheelSlice: Identifiable {
    public let id = UUID()
    public let title: String
    public let color: Color
}

public struct SpinTheWheelView: View {
    public let onDismiss: () -> Void

    @State private var rotationDegrees: Double = 0
    @State private var isSpinning: Bool = false
    @State private var wonReward: String? = nil

    private let slices: [WheelSlice] = [
        WheelSlice(title: "₹50 UPI", color: Color.orange),
        WheelSlice(title: "10% Off", color: Color.purple),
        WheelSlice(title: "Free Delivery", color: Color.blue),
        WheelSlice(title: "₹100 Cashback", color: Color.green),
        WheelSlice(title: "500 Coins", color: Color.yellow),
        WheelSlice(title: "Try Again", color: Color.gray)
    ]

    public init(onDismiss: @escaping () -> Void = {}) {
        self.onDismiss = onDismiss
    }

    public var body: some View {
        NavigationStack {
            ZStack {
                UsColors.bgPrimary
                    .ignoresSafeArea()

                VStack(spacing: 24) {
                    VStack(spacing: 6) {
                        Text("🎡 Spin & Win Daily Rewards")
                            .font(.system(size: 22, weight: .bold))
                            .foregroundColor(UsColors.textPrimary)
                        Text("Spin the fortune wheel to win instant wallet cashback and coupons.")
                            .font(.system(size: 13))
                            .foregroundColor(UsColors.textMuted)
                            .multilineTextAlignment(.center)
                            .padding(.horizontal, 24)
                    }
                    .padding(.top, 12)

                    // Wheel Graphic
                    ZStack {
                        // Outer ring
                        Circle()
                            .stroke(Color.white.opacity(0.3), lineWidth: 8)
                            .frame(width: 280, height: 280)

                        // Slices
                        ForEach(Array(slices.enumerated()), id: \.offset) { idx, slice in
                            let angle = Double(idx) * (360.0 / Double(slices.count))
                            VStack {
                                Text(slice.title)
                                    .font(.system(size: 12, weight: .black))
                                    .foregroundColor(.white)
                                    .rotationEffect(.degrees(-90))
                                    .padding(.top, 16)
                                Spacer()
                            }
                            .frame(width: 260, height: 260)
                            .rotationEffect(.degrees(angle))
                        }

                        // Center Spinner Pin
                        Circle()
                            .fill(Color.white)
                            .frame(width: 44, height: 44)
                            .shadow(radius: 6)

                        Image(systemName: "star.fill")
                            .foregroundColor(Color.orange)
                            .font(.system(size: 20))
                    }
                    .rotationEffect(.degrees(rotationDegrees))
                    .frame(width: 280, height: 280)

                    // Reward text
                    if let reward = wonReward {
                        VStack(spacing: 4) {
                            Text("🎉 Congratulations!")
                                .font(.system(size: 16, weight: .bold))
                                .foregroundColor(UsColors.onlineGreen)
                            Text("You won: \(reward)")
                                .font(.system(size: 18, weight: .black, design: .rounded))
                                .foregroundColor(UsColors.textPrimary)
                        }
                    }

                    Spacer()

                    // Spin Button
                    Button(action: spinWheel) {
                        HStack {
                            Spacer()
                            Text(isSpinning ? "Spinning..." : "Spin the Wheel (Free)")
                                .font(.system(size: 16, weight: .bold))
                                .foregroundColor(.black)
                            Spacer()
                        }
                        .padding(.vertical, 16)
                        .background(Color.white)
                        .clipShape(RoundedRectangle(cornerRadius: 14))
                    }
                    .disabled(isSpinning)
                    .padding(16)
                }
            }
            .navigationBarTitleDisplayMode(.inline)
            .toolbar {
                ToolbarItem(placement: .cancellationAction) {
                    Button("Close", action: onDismiss)
                        .foregroundColor(UsColors.textMuted)
                }
            }
        }
    }

    private func spinWheel() {
        guard !isSpinning else { return }
        isSpinning = true
        wonReward = nil
        HapticManager.shared.trigger(.medium)

        let extraDegrees = Double.random(in: 720...1440)
        withAnimation(.easeOut(duration: 3.5)) {
            rotationDegrees += extraDegrees
        }

        DispatchQueue.main.asyncAfter(deadline: .now() + 3.6) {
            isSpinning = false
            let reward = "₹100 Cashback on UPI"
            wonReward = reward
            HapticManager.shared.trigger(.success)
            ToastManager.shared.show("🎉 \(reward) credited to your US Wallet!", style: .success)
        }
    }
}
