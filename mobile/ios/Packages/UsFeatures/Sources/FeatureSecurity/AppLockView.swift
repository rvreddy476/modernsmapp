import SwiftUI
import LocalAuthentication
import UsDesignSystem

public struct AppLockView: View {
    public let onUnlock: () -> Void

    @State private var pin: String = ""
    @State private var errorMessage: String? = nil
    private let correctPin = "1234"

    public init(onUnlock: @escaping () -> Void = {}) {
        self.onUnlock = onUnlock
    }

    public var body: some View {
        ZStack {
            Color.black.ignoresSafeArea()

            VStack(spacing: 32) {
                Spacer()

                // Lock Icon & Header
                VStack(spacing: 12) {
                    Image(systemName: "lock.shield.fill")
                        .font(.system(size: 54))
                        .foregroundColor(UsColors.postbookPrimary)

                    Text("US Secure Lock")
                        .font(.system(size: 24, weight: .bold))
                        .foregroundColor(.white)

                    Text("Unlock to access your Wallet and private messages")
                        .font(.system(size: 13))
                        .foregroundColor(.white.opacity(0.7))
                        .multilineTextAlignment(.center)
                        .padding(.horizontal, 32)
                }

                // PIN Dots Indicator
                HStack(spacing: 16) {
                    ForEach(0..<4, id: \.self) { idx in
                        Circle()
                            .fill(idx < pin.count ? Color.white : Color.white.opacity(0.2))
                            .frame(width: 16, height: 16)
                    }
                }
                .padding(.vertical, 12)

                if let err = errorMessage {
                    Text(err)
                        .font(.system(size: 13))
                        .foregroundColor(UsColors.statusError)
                }

                // Number Pad (1-9, FaceID, 0, Backspace)
                VStack(spacing: 18) {
                    ForEach(0..<3) { row in
                        HStack(spacing: 28) {
                            ForEach(1...3, id: \.self) { col in
                                let num = row * 3 + col
                                numButton("\(num)")
                            }
                        }
                    }

                    HStack(spacing: 28) {
                        // Face ID Button
                        Button(action: authenticateWithBiometrics) {
                            ZStack {
                                Circle().fill(Color.white.opacity(0.1)).frame(width: 72, height: 72)
                                Image(systemName: "faceid")
                                    .font(.system(size: 28))
                                    .foregroundColor(.white)
                            }
                        }

                        numButton("0")

                        // Backspace Button
                        Button(action: {
                            if !pin.isEmpty { pin.removeLast() }
                        }) {
                            ZStack {
                                Circle().fill(Color.white.opacity(0.1)).frame(width: 72, height: 72)
                                Image(systemName: "delete.left.fill")
                                    .font(.system(size: 22))
                                    .foregroundColor(.white)
                            }
                        }
                    }
                }

                Spacer()
            }
            .padding(24)
        }
        .onAppear {
            authenticateWithBiometrics()
        }
    }

    private func numButton(_ digit: String) -> some View {
        Button(action: {
            guard pin.count < 4 else { return }
            pin.append(digit)
            HapticManager.shared.trigger(.light)
            if pin.count == 4 {
                verifyPin()
            }
        }) {
            ZStack {
                Circle()
                    .fill(Color.white.opacity(0.1))
                    .frame(width: 72, height: 72)

                Text(digit)
                    .font(.system(size: 26, weight: .bold, design: .rounded))
                    .foregroundColor(.white)
            }
        }
    }

    private func verifyPin() {
        if pin == correctPin {
            HapticManager.shared.trigger(.success)
            onUnlock()
        } else {
            HapticManager.shared.trigger(.error)
            errorMessage = "Incorrect PIN. Please try again."
            pin = ""
        }
    }

    private func authenticateWithBiometrics() {
        let context = LAContext()
        var error: NSError?
        if context.canEvaluatePolicy(.deviceOwnerAuthenticationWithBiometrics, error: &error) {
            context.evaluatePolicy(.deviceOwnerAuthenticationWithBiometrics, localizedReason: "Unlock US App") { success, _ in
                if success {
                    DispatchQueue.main.async {
                        HapticManager.shared.trigger(.success)
                        onUnlock()
                    }
                }
            }
        }
    }
}
