import SwiftUI

public enum UsAvatarSize {
    case small
    case medium
    case large

    public var dimension: CGFloat {
        switch self {
        case .small: return 32
        case .medium: return 40
        case .large: return 56
        }
    }

    public var fontSize: CGFloat {
        switch self {
        case .small: return 12
        case .medium: return 15
        case .large: return 20
        }
    }
}

public struct UsAvatar: View {
    public let name: String
    public let url: String?
    public let size: UsAvatarSize

    public init(name: String, url: String? = nil, size: UsAvatarSize = .medium) {
        self.name = name
        self.url = url
        self.size = size
    }

    private var initial: String {
        guard let first = name.trimmingCharacters(in: .whitespacesAndNewlines).first else {
            return "?"
        }
        return String(first).uppercased()
    }

    public var body: some View {
        ZStack {
            if let urlString = url, let imageUrl = URL(string: urlString) {
                AsyncImage(url: imageUrl) { phase in
                    switch phase {
                    case .success(let image):
                        image
                            .resizable()
                            .scaledToFill()
                    default:
                        placeholder
                    }
                }
            } else {
                placeholder
            }
        }
        .frame(width: size.dimension, height: size.dimension)
        .clipShape(Circle())
        .overlay(
            Circle()
                .stroke(UsColors.borderMedium, lineWidth: 0.5)
        )
    }

    private var placeholder: some View {
        ZStack {
            Circle()
                .fill(
                    LinearGradient(
                        colors: [Color(red: 0x2A / 255.0, green: 0x2A / 255.0, blue: 0x36 / 255.0),
                                 Color(red: 0x1A / 255.0, green: 0x1A / 255.0, blue: 0x24 / 255.0)],
                        startPoint: .topLeading,
                        endPoint: .bottomTrailing
                    )
                )
            Text(initial)
                .font(.system(size: size.fontSize, weight: .bold))
                .foregroundColor(UsColors.textPrimary)
        }
    }
}
