import SwiftUI

public enum UsColors {
    // Backgrounds
    public static let bgPrimary = Color(red: 0, green: 0, blue: 0)
    public static let bgSecondary = Color(red: 0x12 / 255.0, green: 0x12 / 255.0, blue: 0x12 / 255.0)
    public static let bgTertiary = Color(red: 0x1C / 255.0, green: 0x1C / 255.0, blue: 0x1E / 255.0)
    public static let bgCard = Color.white.opacity(0.04)
    public static let bgCardHover = Color.white.opacity(0.06)

    // Borders
    public static let borderSubtle = Color.white.opacity(0.06)
    public static let borderMedium = Color.white.opacity(0.12)
    public static let borderStrong = Color.white.opacity(0.20)

    // Text Ramp
    public static let textPrimary = Color.white
    public static let textSecondary = Color(red: 0xE5 / 255.0, green: 0xE5 / 255.0, blue: 0xEA / 255.0)
    public static let textTertiary = Color(red: 0xD1 / 255.0, green: 0xD1 / 255.0, blue: 0xD6 / 255.0)
    public static let textMuted = Color(red: 0x8E / 255.0, green: 0x8E / 255.0, blue: 0x93 / 255.0)
    public static let textDim = Color(red: 0x63 / 255.0, green: 0x63 / 255.0, blue: 0x66 / 255.0)

    // Brand Colors
    public static let postbookPrimary = Color(red: 1.0, green: 0x6B / 255.0, blue: 0x35 / 255.0)
    public static let postbookSecondary = Color(red: 1.0, green: 0x8F / 255.0, blue: 0x65 / 255.0)
    public static let postgramPrimary = Color(red: 1.0, green: 0x33 / 255.0, blue: 0x66 / 255.0)
    public static let postgramSecondary = Color(red: 0xC8 / 255.0, green: 0x50 / 255.0, blue: 0xC0 / 255.0)
    public static let posttubePrimary = Color(red: 0x4E / 255.0, green: 0xCD / 255.0, blue: 0xC4 / 255.0)

    // Status
    public static let statusError = Color(red: 1.0, green: 0x47 / 255.0, blue: 0x57 / 255.0)
    public static let statusSuccess = Color(red: 0x2E / 255.0, green: 0xD5 / 255.0, blue: 0x73 / 255.0)
    public static let statusWarning = Color(red: 1.0, green: 0xAB / 255.0, blue: 0.0)

    // Presence
    public static let liveRed = Color(red: 1.0, green: 0x33 / 255.0, blue: 0x66 / 255.0)
    public static let onlineGreen = Color(red: 0x4E / 255.0, green: 0xCD / 255.0, blue: 0xC4 / 255.0)
}
