import SwiftUI

public struct UsLoadingState: View {
    public let message: String

    public init(message: String = "Loading...") {
        self.message = message
    }

    public var body: some View {
        VStack(spacing: 16) {
            ProgressView()
                .tint(UsColors.postbookPrimary)
                .scaleEffect(1.2)
            Text(message)
                .font(.system(size: 14, weight: .medium))
                .foregroundColor(UsColors.textMuted)
        }
        .frame(maxWidth: .infinity, maxHeight: .infinity)
        .padding()
    }
}

public struct UsErrorState: View {
    public let message: String
    public let onRetry: () -> Void

    public init(message: String, onRetry: @escaping () -> Void) {
        self.message = message
        self.onRetry = onRetry
    }

    public var body: some View {
        VStack(spacing: 16) {
            Image(systemName: "exclamationmark.triangle")
                .font(.system(size: 36))
                .foregroundColor(UsColors.statusError)
            Text(message)
                .font(.system(size: 15, weight: .medium))
                .foregroundColor(UsColors.textPrimary)
                .multilineTextAlignment(.center)
            Button("Try Again", action: onRetry)
                .font(.system(size: 14, weight: .semibold))
                .foregroundColor(.black)
                .padding(.horizontal, 20)
                .padding(.vertical, 10)
                .background(Color.white)
                .clipShape(Capsule())
        }
        .frame(maxWidth: .infinity, maxHeight: .infinity)
        .padding(24)
    }
}

public struct UsEmptyState: View {
    public let title: String
    public let detail: String

    public init(title: String, detail: String) {
        self.title = title
        self.detail = detail
    }

    public var body: some View {
        VStack(spacing: 8) {
            Text(title)
                .font(.system(size: 17, weight: .bold))
                .foregroundColor(UsColors.textPrimary)
            Text(detail)
                .font(.system(size: 14, weight: .regular))
                .foregroundColor(UsColors.textMuted)
                .multilineTextAlignment(.center)
        }
        .frame(maxWidth: .infinity, maxHeight: .infinity)
        .padding(24)
    }
}
