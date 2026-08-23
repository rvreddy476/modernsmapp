import SwiftUI

public enum ToastStyle {
    case info
    case success
    case error
    case warning

    var icon: String {
        switch self {
        case .info: return "info.circle.fill"
        case .success: return "checkmark.circle.fill"
        case .error: return "exclamationmark.circle.fill"
        case .warning: return "exclamationmark.triangle.fill"
        }
    }

    var color: Color {
        switch self {
        case .info: return UsColors.posttubePrimary
        case .success: return UsColors.statusSuccess
        case .error: return UsColors.statusError
        case .warning: return UsColors.statusWarning
        }
    }
}

public struct ToastMessage: Identifiable, Equatable {
    public let id = UUID()
    public let title: String
    public let message: String?
    public let style: ToastStyle

    public init(title: String, message: String? = nil, style: ToastStyle = .info) {
        self.title = title
        self.message = message
        self.style = style
    }

    public static func == (lhs: ToastMessage, rhs: ToastMessage) -> Bool {
        lhs.id == rhs.id
    }
}

@Observable
public final class ToastManager: @unchecked Sendable {
    public static let shared = ToastManager()

    public var currentToast: ToastMessage?

    public init() {}

    @MainActor
    public func show(_ title: String, message: String? = nil, style: ToastStyle = .info) {
        currentToast = ToastMessage(title: title, message: message, style: style)
        HapticManager.shared.trigger(style == .error ? .error : .success)

        DispatchQueue.main.asyncAfter(deadline: .now() + 3.0) { [weak self] in
            if self?.currentToast?.title == title {
                self?.currentToast = nil
            }
        }
    }
}

public struct ToastView: View {
    public let toast: ToastMessage

    public init(toast: ToastMessage) {
        self.toast = toast
    }

    public var body: some View {
        HStack(spacing: 12) {
            Image(systemName: toast.style.icon)
                .font(.system(size: 20))
                .foregroundColor(toast.style.color)

            VStack(alignment: .leading, spacing: 2) {
                Text(toast.title)
                    .font(.system(size: 14, weight: .bold))
                    .foregroundColor(UsColors.textPrimary)

                if let msg = toast.message {
                    Text(msg)
                        .font(.system(size: 12))
                        .foregroundColor(UsColors.textSecondary)
                        .lineLimit(2)
                }
            }

            Spacer()
        }
        .padding(.horizontal, 16)
        .padding(.vertical, 12)
        .background(UsColors.bgSecondary)
        .clipShape(RoundedRectangle(cornerRadius: 14))
        .overlay(RoundedRectangle(cornerRadius: 14).stroke(UsColors.borderMedium, lineWidth: 1))
        .shadow(color: Color.black.opacity(0.4), radius: 12, x: 0, y: 6)
        .padding(.horizontal, 16)
    }
}
