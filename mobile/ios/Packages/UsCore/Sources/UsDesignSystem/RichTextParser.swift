import SwiftUI

public enum RichTextParser {
    public static func parse(text: String, primaryColor: Color = UsColors.textPrimary, accentColor: Color = UsColors.postbookPrimary) -> AttributedString {
        var attributed = AttributedString(text)
        attributed.foregroundColor = primaryColor

        // Regex for @mentions and #hashtags
        let pattern = "(@[a-zA-Z0-9_]+|#[a-zA-Z0-9_]+)"
        guard let regex = try? NSRegularExpression(pattern: pattern, options: []) else {
            return attributed
        }

        let nsString = text as NSString
        let matches = regex.matches(in: text, options: [], range: NSRange(location: 0, length: nsString.length))

        for match in matches {
            if let range = Range(match.range, in: text),
               let attributedRange = Range(range, in: attributed) {
                attributed[attributedRange].foregroundColor = accentColor
                attributed[attributedRange].inlinePresentationIntent = .stronglyEmphasized
            }
        }

        return attributed
    }
}

public struct RichTextView: View {
    public let text: String
    public let onMentionTap: ((String) -> Void)?
    public let onHashtagTap: ((String) -> Void)?

    public init(
        text: String,
        onMentionTap: ((String) -> Void)? = nil,
        onHashtagTap: ((String) -> Void)? = nil
    ) {
        self.text = text
        self.onMentionTap = onMentionTap
        self.onHashtagTap = onHashtagTap
    }

    public var body: some View {
        Text(RichTextParser.parse(text: text))
            .font(.system(size: 15))
            .lineSpacing(3)
    }
}
