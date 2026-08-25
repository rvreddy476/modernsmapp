import SwiftUI
import UsModel
import UsDesignSystem

public enum AppThemePreset: String, CaseIterable, Identifiable {
    case defaultDark = "Default Dark"
    case oledBlack = "OLED Pitch Black"
    case cyberpunk = "Cyberpunk Purple"
    case emerald = "Emerald Green"
    case sunset = "Sunset Amber"

    public var id: String { rawValue }

    public var accentColor: Color {
        switch self {
        case .defaultDark: return UsColors.postbookPrimary
        case .oledBlack: return Color.white
        case .cyberpunk: return Color.purple
        case .emerald: return UsColors.onlineGreen
        case .sunset: return Color.orange
        }
    }
}

public struct ThemePickerView: View {
    public let onDismiss: () -> Void

    @State private var selectedTheme: AppThemePreset = .defaultDark

    public init(onDismiss: @escaping () -> Void = {}) {
        self.onDismiss = onDismiss
    }

    public var body: some View {
        NavigationStack {
            ZStack {
                UsColors.bgPrimary
                    .ignoresSafeArea()

                ScrollView {
                    VStack(spacing: 16) {
                        Text("Customize your US App aesthetic")
                            .font(.system(size: 13))
                            .foregroundColor(UsColors.textMuted)

                        VStack(spacing: 12) {
                            ForEach(AppThemePreset.allCases) { theme in
                                themeRow(theme)
                            }
                        }
                    }
                    .padding(16)
                }
            }
            .navigationTitle("App Theme")
            .navigationBarTitleDisplayMode(.inline)
            .toolbar {
                ToolbarItem(placement: .cancellationAction) {
                    Button("Close", action: onDismiss)
                        .foregroundColor(UsColors.textMuted)
                }
            }
        }
    }

    @ViewBuilder
    private func themeRow(_ theme: AppThemePreset) -> some View {
        let isSelected = selectedTheme == theme
        Button(action: {
            selectedTheme = theme
            HapticManager.shared.trigger(.selection)
            ToastManager.shared.show("Theme changed to \(theme.rawValue)", style: .success)
        }) {
            HStack(spacing: 14) {
                Circle()
                    .fill(theme.accentColor)
                    .frame(width: 28, height: 28)
                    .overlay(Circle().stroke(Color.white.opacity(0.3), lineWidth: 1))

                Text(theme.rawValue)
                    .font(.system(size: 15, weight: .semibold))
                    .foregroundColor(UsColors.textPrimary)

                Spacer()

                if isSelected {
                    Image(systemName: "checkmark.circle.fill")
                        .font(.system(size: 20))
                        .foregroundColor(theme.accentColor)
                }
            }
            .padding(16)
            .background(UsColors.bgSecondary)
            .clipShape(RoundedRectangle(cornerRadius: 14))
            .overlay(
                RoundedRectangle(cornerRadius: 14)
                    .stroke(isSelected ? theme.accentColor : UsColors.borderSubtle, lineWidth: isSelected ? 1.5 : 1)
            )
        }
        .buttonStyle(.plain)
    }
}
