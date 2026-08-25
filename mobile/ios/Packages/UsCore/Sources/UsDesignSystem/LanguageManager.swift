import SwiftUI

public enum AppLanguage: String, CaseIterable, Identifiable {
    case english = "en"
    case hindi = "hi"
    case tamil = "ta"
    case telugu = "te"
    case kannada = "kn"
    case bengali = "bn"

    public var id: String { rawValue }

    public var displayName: String {
        switch self {
        case .english: return "English"
        case .hindi: return "हिंदी (Hindi)"
        case .tamil: return "தமிழ் (Tamil)"
        case .telugu: return "తెలుగు (Telugu)"
        case .kannada: return "ಕನ್ನಡ (Kannada)"
        case .bengali: return "বাংলা (Bengali)"
        }
    }
}

@Observable
public final class LanguageManager: @unchecked Sendable {
    public static let shared = LanguageManager()

    public var currentLanguage: AppLanguage = .english

    public init() {}

    public func setLanguage(_ language: AppLanguage) {
        currentLanguage = language
    }
}
