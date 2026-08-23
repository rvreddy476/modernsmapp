import SwiftUI
import UsModel
import UsDesignSystem

public struct LinkStickerView: View {
    public let urlString: String
    public let customTitle: String?
    public let onOpenLink: (URL) -> Void

    public init(
        urlString: String = "https://us.app/creator/sarah",
        customTitle: String? = "Visit My Creator Store 🛍️",
        onOpenLink: @escaping (URL) -> Void = { _ in }
    ) {
        self.urlString = urlString
        self.customTitle = customTitle
        self.onOpenLink = onOpenLink
    }

    private var domainDisplay: String {
        URL(string: urlString)?.host() ?? "us.app"
    }

    public var body: some View {
        Button(action: {
            if let url = URL(string: urlString) {
                HapticManager.shared.trigger(.selection)
                onOpenLink(url)
            }
        }) {
            HStack(spacing: 8) {
                Image(systemName: "link")
                    .font(.system(size: 14, weight: .bold))
                    .foregroundColor(.black)

                VStack(alignment: .leading, spacing: 1) {
                    Text(customTitle ?? domainDisplay)
                        .font(.system(size: 13, weight: .bold))
                        .foregroundColor(.black)
                        .lineLimit(1)

                    if customTitle != nil {
                        Text(domainDisplay)
                            .font(.system(size: 10, weight: .medium))
                            .foregroundColor(Color.black.opacity(0.6))
                            .lineLimit(1)
                    }
                }

                Image(systemName: "arrow.up.right")
                    .font(.system(size: 11, weight: .bold))
                    .foregroundColor(Color.black.opacity(0.8))
            }
            .padding(.horizontal, 14)
            .padding(.vertical, 10)
            .background(Color.white)
            .clipShape(Capsule())
            .shadow(color: Color.black.opacity(0.25), radius: 6, x: 0, y: 3)
        }
        .buttonStyle(.plain)
    }
}
