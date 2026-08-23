import SwiftUI
import UsModel
import UsDesignSystem

public struct LyricsStickerView: View {
    public let songTitle: String
    public let artistName: String
    public let activeLyric: String

    @State private var isGlowing: Bool = false

    public init(
        songTitle: String = "Chaleya (Feat. Arijit Singh)",
        artistName: String = "Anirudh Ravichander",
        activeLyric: String = "\"Ishq mein dil bana hai, ishq mein dil fana hai...\""
    ) {
        self.songTitle = songTitle
        self.artistName = artistName
        self.activeLyric = activeLyric
    }

    public var body: some View {
        VStack(alignment: .leading, spacing: 8) {
            // Track metadata
            HStack(spacing: 8) {
                Image(systemName: "music.note")
                    .font(.system(size: 11))
                    .foregroundColor(UsColors.postbookPrimary)

                Text("\(songTitle) • \(artistName)")
                    .font(.system(size: 10, weight: .bold))
                    .foregroundColor(.white.opacity(0.8))
                    .lineLimit(1)
            }

            // Glowing karaoke lyric text
            Text(activeLyric)
                .font(.system(size: 16, weight: .bold, design: .rounded))
                .foregroundColor(.white)
                .shadow(color: Color.pink.opacity(isGlowing ? 0.8 : 0.2), radius: isGlowing ? 8 : 2)
                .animation(.easeInOut(duration: 1.2).repeatForever(autoreverses: true), value: isGlowing)
                .onAppear {
                    isGlowing = true
                }
        }
        .padding(.horizontal, 16)
        .padding(.vertical, 12)
        .background(
            LinearGradient(
                colors: [Color.black.opacity(0.85), Color(red: 0x2A/255.0, green: 0x10/255.0, blue: 0x30/255.0)],
                startPoint: .topLeading,
                endPoint: .bottomTrailing
            )
        )
        .clipShape(RoundedRectangle(cornerRadius: 16))
        .overlay(RoundedRectangle(cornerRadius: 16).stroke(Color.pink.opacity(0.3), lineWidth: 1))
        .shadow(color: Color.black.opacity(0.4), radius: 8, x: 0, y: 4)
        .frame(width: 280)
    }
}
