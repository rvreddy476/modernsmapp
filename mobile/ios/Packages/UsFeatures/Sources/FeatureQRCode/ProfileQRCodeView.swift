import SwiftUI
import CoreImage.CIFilterBuiltins
import UsModel
import UsDesignSystem
import UsNetwork

public struct ProfileQRCodeView: View {
    public let username: String
    public let displayName: String
    public let onDismiss: () -> Void

    @State private var selectedTab: Int = 0 // 0 = My QR, 1 = Scanner
    private let context = CIContext()
    private let filter = CIFilter.qrCodeGenerator()

    public init(
        username: String = "alex",
        displayName: String = "Alex Rivera",
        onDismiss: @escaping () -> Void = {}
    ) {
        self.username = username
        self.displayName = displayName
        self.onDismiss = onDismiss
    }

    public var body: some View {
        NavigationStack {
            ZStack {
                UsColors.bgPrimary
                    .ignoresSafeArea()

                VStack(spacing: 24) {
                    // Segmented Selector
                    Picker("Mode", selection: $selectedTab) {
                        Text("My QR Code").tag(0)
                        Text("Scan QR").tag(1)
                    }
                    .pickerStyle(.segmented)
                    .padding(.horizontal, 24)
                    .padding(.top, 12)

                    if selectedTab == 0 {
                        myQRCodeCard
                    } else {
                        qrScannerPlaceholder
                    }

                    Spacer()
                }
            }
            .navigationTitle("QR Code")
            .navigationBarTitleDisplayMode(.inline)
            .toolbar {
                ToolbarItem(placement: .cancellationAction) {
                    Button("Close", action: onDismiss)
                        .foregroundColor(UsColors.textMuted)
                }
            }
        }
    }

    private var myQRCodeCard: some View {
        VStack(spacing: 20) {
            VStack(spacing: 6) {
                UsAvatar(name: displayName, size: .large)
                Text(displayName)
                    .font(.system(size: 18, weight: .bold))
                    .foregroundColor(UsColors.textPrimary)
                Text("@\(username)")
                    .font(.system(size: 14))
                    .foregroundColor(UsColors.textMuted)
            }

            // Generated QR Code
            if let qrImage = generateQRCode(from: "https://app.us.com/u/\(username)") {
                Image(uiImage: qrImage)
                    .interpolation(.none)
                    .resizable()
                    .scaledToFit()
                    .frame(width: 200, height: 200)
                    .padding(16)
                    .background(Color.white)
                    .clipShape(RoundedRectangle(cornerRadius: 16))
            }

            Text("Scan to follow on US or pay via UPI")
                .font(.system(size: 12, weight: .medium))
                .foregroundColor(UsColors.textMuted)
        }
        .padding(28)
        .background(UsColors.bgSecondary)
        .clipShape(RoundedRectangle(cornerRadius: 24))
        .overlay(RoundedRectangle(cornerRadius: 24).stroke(UsColors.borderMedium, lineWidth: 1))
        .padding(.horizontal, 24)
    }

    private var qrScannerPlaceholder: some View {
        VStack(spacing: 16) {
            ZStack {
                Color.black
                VStack(spacing: 12) {
                    Image(systemName: "viewfinder")
                        .font(.system(size: 72))
                        .foregroundColor(UsColors.postbookPrimary)
                    Text("Point camera at any US QR Code")
                        .font(.system(size: 14))
                        .foregroundColor(.white.opacity(0.8))
                }
            }
            .frame(height: 300)
            .clipShape(RoundedRectangle(cornerRadius: 20))
            .padding(.horizontal, 24)
        }
    }

    private func generateQRCode(from string: String) -> UIImage? {
        filter.message = Data(string.utf8)
        if let outputImage = filter.outputImage,
           let cgImage = context.createCGImage(outputImage, from: outputImage.extent) {
            return UIImage(cgImage: cgImage)
        }
        return nil
    }
}
