import SwiftUI
import UsModel
import UsDesignSystem

public enum SubtitleLanguage: String, CaseIterable, Identifiable {
    case english = "English"
    case hindi = "हिंदी (Hindi)"
    case tamil = "தமிழ் (Tamil)"
    case telugu = "తెలుగు (Telugu)"
    case kannada = "ಕನ್ನಡ (Kannada)"
    case bengali = "বাংলা (Bengali)"

    public var id: String { rawValue }
}

public struct LiveSubtitlesView: View {
    @State private var selectedLanguage: SubtitleLanguage = .hindi
    @State private var activeSubtitleText: String = "नमस्ते दोस्तों, आज हम भारत के पहले सुपर-ऐप का डेमो देखने जा रहे हैं!"

    public init() {}

    public var body: some View {
        VStack(spacing: 8) {
            // Language selector pills
            ScrollView(.horizontal, showsIndicators: false) {
                HStack(spacing: 8) {
                    ForEach(SubtitleLanguage.allCases) { lang in
                        let isSelected = selectedLanguage == lang
                        Button(action: {
                            selectedLanguage = lang
                            updateSubtitlesForLanguage(lang)
                            HapticManager.shared.trigger(.selection)
                        }) {
                            Text(lang.rawValue)
                                .font(.system(size: 11, weight: isSelected ? .bold : .medium))
                                .foregroundColor(isSelected ? .black : .white)
                                .padding(.horizontal, 10)
                                .padding(.vertical, 5)
                                .background(isSelected ? Color.white : Color.black.opacity(0.5))
                                .clipShape(Capsule())
                        }
                        .buttonStyle(.plain)
                    }
                }
            }

            // Subtitle Display Banner
            Text(activeSubtitleText)
                .font(.system(size: 13, weight: .bold))
                .foregroundColor(.white)
                .multilineTextAlignment(.center)
                .padding(.horizontal, 14)
                .padding(.vertical, 8)
                .background(Color.black.opacity(0.8))
                .clipShape(RoundedRectangle(cornerRadius: 10))
        }
        .padding(.horizontal, 16)
    }

    private func updateSubtitlesForLanguage(_ lang: SubtitleLanguage) {
        switch lang {
        case .english:
            activeSubtitleText = "Hey everyone, today we are checking out the demo of India's #1 super-app!"
        case .hindi:
            activeSubtitleText = "नमस्ते दोस्तों, आज हम भारत के पहले सुपर-ऐप का डेमो देखने जा रहे हैं!"
        case .tamil:
            activeSubtitleText = "அனைவருக்கும் வணக்கம், இன்று இந்தியாவின் முதன்மை சூப்பர் செயலியை பார்க்கிறோம்!"
        case .telugu:
            activeSubtitleText = "అందరికీ నమస్కారం, ఈ రోజు మనం భారతదేశం యొక్క ప్రముఖ సూపర్-యాప్ డెమో చూస్తున్నాము!"
        case .kannada:
            activeSubtitleText = "ಎಲ್ಲರಿಗೂ ನಮಸ್ಕಾರ, ಇಂದು ನಾವು ಭಾರತದ ಪ್ರಮುಖ ಸೂಪರ್ ಆ್ಯಪ್ ಡೆಮೊ ನೋಡುತ್ತಿದ್ದೇವೆ!"
        case .bengali:
            activeSubtitleText = "সবাইকে নমস্কার, আজ আমরা ভারতের শীর্ষস্থানীয় সুপার অ্যাপের ডেমো দেখতে পাচ্ছি!"
        }
    }
}
