import SwiftUI
import UsModel
import UsDesignSystem

public struct NFCTapCardView: View {
    public let onDismiss: () -> Void

    @State private var isScanning: Bool = false
    @State private var scannedCardNumber: String? = nil
    @State private var cardBalance: String? = nil

    public init(onDismiss: @escaping () -> Void = {}) {
        self.onDismiss = onDismiss
    }

    public var body: some View {
        NavigationStack {
            ZStack {
                UsColors.bgPrimary
                    .ignoresSafeArea()

                VStack(spacing: 28) {
                    Text("Hold your physical Metro / Transit Card near the top of your iPhone")
                        .font(.system(size: 13))
                        .foregroundColor(UsColors.textMuted)
                        .multilineTextAlignment(.center)
                        .padding(.horizontal, 20)

                    // NFC Scanning Animation Graphic
                    ZStack {
                        Circle()
                            .fill(UsColors.postbookPrimary.opacity(isScanning ? 0.2 : 0.05))
                            .frame(width: 180, height: 180)
                            .scaleEffect(isScanning ? 1.2 : 1.0)
                            .animation(isScanning ? .easeInOut(duration: 0.8).repeatForever(autoreverses: true) : .default, value: isScanning)

                        Circle()
                            .fill(UsColors.bgSecondary)
                            .frame(width: 120, height: 120)

                        Image(systemName: "wave.3.forward.circle.fill")
                            .font(.system(size: 48))
                            .foregroundColor(isScanning ? UsColors.postbookPrimary : UsColors.textMuted)
                    }

                    if let card = scannedCardNumber, let balance = cardBalance {
                        VStack(spacing: 8) {
                            Text("Card Detected! 💳")
                                .font(.system(size: 16, weight: .bold))
                                .foregroundColor(UsColors.onlineGreen)

                            Text("Card: \(card)")
                                .font(.system(size: 13, weight: .semibold, design: .monospaced))
                                .foregroundColor(UsColors.textPrimary)

                            Text("Current Balance: \(balance)")
                                .font(.system(size: 20, weight: .black, design: .rounded))
                                .foregroundColor(UsColors.onlineGreen)
                        }
                        .padding(18)
                        .background(UsColors.bgSecondary)
                        .clipShape(RoundedRectangle(cornerRadius: 18))
                    }

                    Spacer()

                    Button(action: startNFCScan) {
                        HStack {
                            Spacer()
                            Text(isScanning ? "Scanning NFC Antenna..." : "Scan Transit Smart Card")
                                .font(.system(size: 15, weight: .bold))
                                .foregroundColor(.black)
                            Spacer()
                        }
                        .padding(.vertical, 16)
                        .background(Color.white)
                        .clipShape(RoundedRectangle(cornerRadius: 14))
                    }
                    .disabled(isScanning)
                    .padding(.horizontal, 16)
                }
                .padding(.top, 24)
            }
            .navigationTitle("NFC Smart Card")
            .navigationBarTitleDisplayMode(.inline)
            .toolbar {
                ToolbarItem(placement: .cancellationAction) {
                    Button("Close", action: onDismiss)
                        .foregroundColor(UsColors.textMuted)
                }
            }
        }
    }

    private func startNFCScan() {
        isScanning = true
        HapticManager.shared.trigger(.selection)
        DispatchQueue.main.asyncAfter(deadline: .now() + 1.5) {
            isScanning = false
            scannedCardNumber = "4820 **** **** 0293"
            cardBalance = "₹184.50"
            HapticManager.shared.trigger(.success)
            ToastManager.shared.show("NFC Smart Card read successfully!", style: .success)
        }
    }
}
