import SwiftUI
import UsModel
import UsDesignSystem
import UsMedia

public struct PodcastEpisode: Identifiable {
    public let id: String
    public let episodeNumber: Int
    public let title: String
    public let durationString: String
    public let releaseDate: String

    public init(id: String, episodeNumber: Int, title: String, durationString: String, releaseDate: String) {
        self.id = id
        self.episodeNumber = episodeNumber
        self.title = title
        self.durationString = durationString
        self.releaseDate = releaseDate
    }
}

public struct PodcastShowView: View {
    public let onDismiss: () -> Void

    @State private var showTitle: String = "The Indian Founder Show 🎙️"
    @State private var hostName: String = "Alex Rivera"
    @State private var episodes: [PodcastEpisode] = [
        PodcastEpisode(id: "ep-1", episodeNumber: 42, title: "EP 42: How We Scaled the Super-App to 50M Users", durationString: "48:12", releaseDate: "Aug 20, 2026"),
        PodcastEpisode(id: "ep-2", episodeNumber: 41, title: "EP 41: Building UPI Micro-Investments in India", durationString: "36:45", releaseDate: "Aug 13, 2026"),
        PodcastEpisode(id: "ep-3", episodeNumber: 40, title: "EP 40: The Future of Creator Collabs & Split Pay", durationString: "52:10", releaseDate: "Aug 06, 2026")
    ]
    @State private var isPlaying: Bool = false
    @State private var playbackSpeed: String = "1.0x"

    public init(onDismiss: @escaping () -> Void = {}) {
        self.onDismiss = onDismiss
    }

    public var body: some View {
        NavigationStack {
            ZStack {
                UsColors.bgPrimary
                    .ignoresSafeArea()

                ScrollView {
                    VStack(alignment: .leading, spacing: 20) {
                        // Podcast Cover Hero
                        HStack(spacing: 16) {
                            ZStack {
                                RoundedRectangle(cornerRadius: 16)
                                    .fill(
                                        LinearGradient(
                                            colors: [Color.purple, Color.blue],
                                            startPoint: .topLeading,
                                            endPoint: .bottomTrailing
                                        )
                                    )
                                    .frame(width: 90, height: 90)

                                Image(systemName: "mic.fill")
                                    .font(.system(size: 36))
                                    .foregroundColor(.white)
                            }

                            VStack(alignment: .leading, spacing: 4) {
                                Text(showTitle)
                                    .font(.system(size: 16, weight: .bold))
                                    .foregroundColor(UsColors.textPrimary)
                                    .lineLimit(2)

                                Text("Hosted by \(hostName)")
                                    .font(.system(size: 12))
                                    .foregroundColor(UsColors.textMuted)

                                Text("Weekly deep dives on Indian tech")
                                    .font(.system(size: 11))
                                    .foregroundColor(UsColors.postbookPrimary)
                            }
                        }
                        .padding(14)
                        .frame(maxWidth: .infinity, alignment: .leading)
                        .background(UsColors.bgSecondary)
                        .clipShape(RoundedRectangle(cornerRadius: 18))

                        // Mini Player Controller Bar
                        HStack {
                            VStack(alignment: .leading, spacing: 2) {
                                Text("Now Playing • EP 42")
                                    .font(.system(size: 10, weight: .bold))
                                    .foregroundColor(UsColors.postbookPrimary)
                                Text("How We Scaled the Super-App...")
                                    .font(.system(size: 13, weight: .semibold))
                                    .foregroundColor(UsColors.textPrimary)
                                    .lineLimit(1)
                            }

                            Spacer()

                            Button(action: {
                                playbackSpeed = playbackSpeed == "1.0x" ? "1.5x" : (playbackSpeed == "1.5x" ? "2.0x" : "1.0x")
                                HapticManager.shared.trigger(.selection)
                            }) {
                                Text(playbackSpeed)
                                    .font(.system(size: 11, weight: .bold))
                                    .foregroundColor(UsColors.textPrimary)
                                    .padding(.horizontal, 8)
                                    .padding(.vertical, 4)
                                    .background(UsColors.bgTertiary)
                                    .clipShape(Capsule())
                            }

                            Button(action: {
                                isPlaying.toggle()
                                HapticManager.shared.trigger(.selection)
                            }) {
                                Image(systemName: isPlaying ? "pause.fill" : "play.fill")
                                    .font(.system(size: 16))
                                    .foregroundColor(.black)
                                    .padding(10)
                                    .background(Color.white)
                                    .clipShape(Circle())
                            }
                        }
                        .padding(14)
                        .background(UsColors.bgSecondary)
                        .clipShape(RoundedRectangle(cornerRadius: 14))

                        Text("Recent Episodes (\(episodes.count))")
                            .font(.system(size: 16, weight: .bold))
                            .foregroundColor(UsColors.textPrimary)

                        LazyVStack(spacing: 12) {
                            ForEach(episodes) { ep in
                                episodeRow(ep)
                            }
                        }
                    }
                    .padding(16)
                }
            }
            .navigationTitle("Podcast Show")
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
    private func episodeRow(_ ep: PodcastEpisode) -> some View {
        VStack(alignment: .leading, spacing: 6) {
            HStack {
                Text(ep.releaseDate)
                    .font(.system(size: 11))
                    .foregroundColor(UsColors.textMuted)

                Spacer()

                Text(ep.durationString)
                    .font(.system(size: 11, weight: .semibold, design: .monospaced))
                    .foregroundColor(UsColors.textPrimary)
            }

            Text(ep.title)
                .font(.system(size: 14, weight: .bold))
                .foregroundColor(UsColors.textPrimary)
        }
        .padding(14)
        .background(UsColors.bgSecondary)
        .clipShape(RoundedRectangle(cornerRadius: 14))
    }
}
