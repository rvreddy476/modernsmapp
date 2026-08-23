import SwiftUI
import UsModel
import UsDesignSystem

public struct MusicStickerView: View {
    public let trackTitle: String
    public let artistName: String
    public let albumArtUrl: String?

    @State private var rotationAngle: Double = 0
    @State private var isPlaying: Bool = true

    public init(
        trackTitle: String = "Midnight Chai (Acoustic)",
        artistName: String = "Prateek Kuhad",
        albumArtUrl: String? = "https://images.unsplash.com/photo-1511671782779-c97d3d27a1d4?w=400"
    ) {
        self.trackTitle = trackTitle
        self.artistName = artistName
        self.albumArtUrl = albumArtUrl
    }

    public var body: some View {
        HStack(spacing: 12) {
            // Rotating Vinyl Disc with Album Art
            ZStack {
                // Vinyl Grooves
                Circle()
                    .fill(Color.black)
                    .frame(width: 44, height: 44)
                    .overlay(Circle().stroke(Color.white.opacity(0.2), lineWidth: 1))

                // Center Album Artwork
                if let urlStr = albumArtUrl, let url = URL(string: urlStr) {
                    AsyncImage(url: url) { phase in
                        switch phase {
                        case .success(let img):
                            img.resizable().scaledToFill()
                        default:
                            Circle().fill(UsColors.postbookPrimary)
                        }
                    }
                    .frame(width: 22, height: 22)
                    .clipShape(Circle())
                } else {
                    Circle()
                        .fill(UsColors.postbookPrimary)
                        .frame(width: 22, height: 22)
                }

                // Center spindle hole
                Circle()
                    .fill(Color.white)
                    .frame(width: 6, height: 6)
            }
            .rotationEffect(.degrees(rotationAngle))
            .shadow(radius: 4)

            // Song Info & Animated Wavebars
            VStack(alignment: .leading, spacing: 2) {
                Text(trackTitle)
                    .font(.system(size: 13, weight: .bold))
                    .foregroundColor(.white)
                    .lineLimit(1)

                Text(artistName)
                    .font(.system(size: 11))
                    .foregroundColor(.white.opacity(0.8))
                    .lineLimit(1)
            }

            Spacer()

            // Animated Equalizer Waveform
            HStack(spacing: 2) {
                ForEach(0..<4, id: \.self) { idx in
                    EqualizerBar(isAnimating: isPlaying, delay: Double(idx) * 0.15)
                }
            }
        }
        .padding(.horizontal, 14)
        .padding(.vertical, 8)
        .background(Color.black.opacity(0.75))
        .clipShape(Capsule())
        .overlay(Capsule().stroke(Color.white.opacity(0.2), lineWidth: 1))
        .frame(width: 260)
        .onAppear {
            withAnimation(.linear(duration: 4.0).repeatForever(autoreverses: false)) {
                rotationAngle = 360
            }
        }
    }
}

private struct EqualizerBar: View {
    public let isAnimating: Bool
    public let delay: Double
    @State private var height: CGFloat = 8

    var body: some View {
        Capsule()
            .fill(Color.white)
            .frame(width: 3, height: height)
            .onAppear {
                withAnimation(.easeInOut(duration: 0.4).repeatForever(autoreverses: true).delay(delay)) {
                    height = 20
                }
            }
    }
}
