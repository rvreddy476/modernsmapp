import SwiftUI
import UsModel
import UsDesignSystem

public enum VerificationType {
    case blueVerified
    case goldCreator
    case greenOfficial

    public var color: Color {
        switch self {
        case .blueVerified: return UsColors.postbookPrimary
        case .goldCreator: return Color.yellow
        case .greenOfficial: return UsColors.onlineGreen
        }
    }

    public var tooltip: String {
        switch self {
        case .blueVerified: return "Verified Identity"
        case .goldCreator: return "Verified Creator / Organization"
        case .greenOfficial: return "Official Entity"
        }
    }
}

public struct VerificationBadgeView: View {
    public let type: VerificationType
    public let size: CGFloat

    public init(type: VerificationType = .blueVerified, size: CGFloat = 14) {
        self.type = type
        self.size = size
    }

    public var body: some View {
        Image(systemName: "checkmark.seal.fill")
            .font(.system(size: size))
            .foregroundColor(type.color)
    }
}
