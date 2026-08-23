import SwiftUI
import UsModel
import UsDesignSystem

public enum ARFilterEffect: String, CaseIterable, Identifiable {
    case none = "None"
    case beauty = "Glow ✨"
    case cyber = "Neon 🕶️"
    case retro = "VHS 📼"
    case sparkle = "Stars ⭐️"

    public var id: String { rawValue }
}

public struct ARCameraView: View {
    public let onCapture: (UIImage) -> Void
    public let onDismiss: () -> Void

    @State private var selectedEffect: ARFilterEffect = .beauty
    @State private var isFrontCamera: Bool = true
    @State private var isFlashOn: Bool = false

    public init(
        onCapture: @escaping (UIImage) -> Void = { _ in },
        onDismiss: @escaping () -> Void = {}
    ) {
        self.onCapture = onCapture
        self.onDismiss = onDismiss
    }

    public var body: some View {
        ZStack {
            Color.black.ignoresSafeArea()

            // Viewfinder Simulator with AR overlays
            ZStack {
                Rectangle()
                    .fill(Color(red: 0x14/255.0, green: 0x14/255.0, blue: 0x20/255.0))

                // AR Filter Overlay Elements
                switch selectedEffect {
                case .beauty:
                    RadialGradient(
                        colors: [Color.pink.opacity(0.15), Color.clear],
                        center: .center,
                        startRadius: 50,
                        endRadius: 200
                    )
                case .cyber:
                    VStack {
                        Image(systemName: "eyeglasses")
                            .font(.system(size: 64))
                            .foregroundColor(Color.cyan)
                            .shadow(color: Color.cyan, radius: 10)
                            .offset(y: -40)
                    }
                case .retro:
                    VStack {
                        HStack {
                            Text("REC ●")
                                .font(.system(size: 13, weight: .bold, design: .monospaced))
                                .foregroundColor(.red)
                            Spacer()
                            Text("SP 0:00:12")
                                .font(.system(size: 13, weight: .bold, design: .monospaced))
                                .foregroundColor(.white)
                        }
                        .padding(20)
                        Spacer()
                    }
                case .sparkle:
                    Image(systemName: "sparkles")
                        .font(.system(size: 54))
                        .foregroundColor(.yellow)
                        .shadow(color: .yellow, radius: 12)
                case .none:
                    EmptyView()
                }
            }
            .clipShape(RoundedRectangle(cornerRadius: 24))
            .padding(.top, 44)
            .padding(.bottom, 120)

            // Top Camera Controls
            VStack {
                HStack(spacing: 20) {
                    Button(action: onDismiss) {
                        Image(systemName: "xmark")
                            .font(.system(size: 20, weight: .bold))
                            .foregroundColor(.white)
                            .padding(10)
                            .background(Color.black.opacity(0.5))
                            .clipShape(Circle())
                    }

                    Spacer()

                    Button(action: { isFlashOn.toggle() }) {
                        Image(systemName: isFlashOn ? "bolt.fill" : "bolt.slash.fill")
                            .font(.system(size: 18))
                            .foregroundColor(isFlashOn ? .yellow : .white)
                            .padding(10)
                            .background(Color.black.opacity(0.5))
                            .clipShape(Circle())
                    }

                    Button(action: { isFrontCamera.toggle() }) {
                        Image(systemName: "camera.rotate.fill")
                            .font(.system(size: 18))
                            .foregroundColor(.white)
                            .padding(10)
                            .background(Color.black.opacity(0.5))
                            .clipShape(Circle())
                    }
                }
                .padding(.horizontal, 20)
                .padding(.top, 8)

                Spacer()

                // Bottom AR Filter Carousel & Shutter Button
                VStack(spacing: 16) {
                    // AR Effects Carousel
                    ScrollView(.horizontal, showsIndicators: false) {
                        HStack(spacing: 14) {
                            ForEach(ARFilterEffect.allCases) { effect in
                                Button(action: {
                                    selectedEffect = effect
                                    HapticManager.shared.trigger(.selection)
                                }) {
                                    Text(effect.rawValue)
                                        .font(.system(size: 13, weight: selectedEffect == effect ? .bold : .medium))
                                        .foregroundColor(selectedEffect == effect ? .black : .white)
                                        .padding(.horizontal, 16)
                                        .padding(.vertical, 8)
                                        .background(selectedEffect == effect ? Color.white : Color.black.opacity(0.6))
                                        .clipShape(Capsule())
                                        .overlay(Capsule().stroke(Color.white.opacity(0.3), lineWidth: 1))
                                }
                                .buttonStyle(.plain)
                            }
                        }
                        .padding(.horizontal, 20)
                    }

                    // Shutter Trigger
                    Button(action: {
                        HapticManager.shared.trigger(.success)
                        ToastManager.shared.show("Photo Captured with \(selectedEffect.rawValue)!", style: .success)
                        onDismiss()
                    }) {
                        ZStack {
                            Circle().stroke(Color.white, lineWidth: 4).frame(width: 74, height: 74)
                            Circle().fill(Color.white).frame(width: 60, height: 60)
                        }
                    }
                    .padding(.bottom, 24)
                }
            }
        }
    }
}
