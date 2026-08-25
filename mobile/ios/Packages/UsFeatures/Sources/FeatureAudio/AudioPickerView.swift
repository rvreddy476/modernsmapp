import SwiftUI
import AVFoundation
import UsModel
import UsDesignSystem
import UsNetwork

@Observable
public final class AudioPickerViewModel: @unchecked Sendable {
    public var tracks: [AudioTrack] = []
    public var searchQuery: String = ""
    public var currentlyPlayingId: String? = nil
    public var isLoading: Bool = false

    private var audioPlayer: AVPlayer?
    private let client: APIClientProtocol

    public init(client: APIClientProtocol = APIClient()) {
        self.client = client
        populateDefaultTracks()
    }

    public var filteredTracks: [AudioTrack] {
        let clean = searchQuery.trimmingCharacters(in: .whitespacesAndNewlines).lowercased()
        guard !clean.isEmpty else { return tracks }
        return tracks.filter {
            $0.title.lowercased().contains(clean) ||
            $0.artist.lowercased().contains(clean)
        }
    }

    public func togglePlay(track: AudioTrack) {
        if currentlyPlayingId == track.id {
            audioPlayer?.pause()
            currentlyPlayingId = nil
        } else {
            if let preview = track.previewUrl, let url = URL(string: preview) {
                audioPlayer = AVPlayer(url: url)
                audioPlayer?.play()
                currentlyPlayingId = track.id
            } else {
                currentlyPlayingId = track.id
            }
        }
    }

    public func stop() {
        audioPlayer?.pause()
        audioPlayer = nil
        currentlyPlayingId = nil
    }

    private func populateDefaultTracks() {
        tracks = [
            AudioTrack(id: "t1", title: "Cyber Sunset", artist: "SynthWave Collective", duration: 30.0, usageCount: 48200),
            AudioTrack(id: "t2", title: "Midnight Lo-Fi Beats", artist: "ChillHop Dreamers", duration: 45.0, usageCount: 125000),
            AudioTrack(id: "t3", title: "Energy Pulse", artist: "Hyperdrive", duration: 15.0, usageCount: 89000),
            AudioTrack(id: "t4", title: "Deep Focus Ambient", artist: "Komorebi Sound", duration: 60.0, usageCount: 34100)
        ]
    }
}

public struct AudioPickerView: View {
    @State private var viewModel = AudioPickerViewModel()
    public let onSelectTrack: (AudioTrack) -> Void
    public let onDismiss: () -> Void

    public init(
        onSelectTrack: @escaping (AudioTrack) -> Void,
        onDismiss: @escaping () -> Void = {}
    ) {
        self.onSelectTrack = onSelectTrack
        self.onDismiss = onDismiss
    }

    public var body: some View {
        NavigationStack {
            ZStack {
                UsColors.bgPrimary
                    .ignoresSafeArea()

                VStack(spacing: 0) {
                    // Search
                    HStack(spacing: 8) {
                        Image(systemName: "magnifyingglass")
                            .foregroundColor(UsColors.textMuted)
                        TextField("Search sounds & music", text: $viewModel.searchQuery)
                            .textFieldStyle(.plain)
                            .font(.system(size: 14))
                            .foregroundColor(UsColors.textPrimary)
                    }
                    .padding(.horizontal, 12)
                    .padding(.vertical, 8)
                    .background(UsColors.bgSecondary)
                    .clipShape(RoundedRectangle(cornerRadius: 10))
                    .padding(.horizontal, 16)
                    .padding(.vertical, 8)

                    List(viewModel.filteredTracks) { track in
                        trackRow(track)
                            .listRowBackground(UsColors.bgPrimary)
                            .listRowSeparatorTint(UsColors.borderSubtle)
                    }
                    .listStyle(.plain)
                    .scrollContentBackground(.hidden)
                }
            }
            .navigationTitle("Audio")
            .navigationBarTitleDisplayMode(.inline)
            .toolbar {
                ToolbarItem(placement: .cancellationAction) {
                    Button("Cancel", action: onDismiss)
                        .foregroundColor(UsColors.textMuted)
                }
            }
            .onDisappear {
                viewModel.stop()
            }
        }
    }

    @ViewBuilder
    private func trackRow(_ track: AudioTrack) -> some View {
        HStack(spacing: 12) {
            // Play / Pause Button
            Button(action: { viewModel.togglePlay(track: track) }) {
                ZStack {
                    RoundedRectangle(cornerRadius: 8)
                        .fill(UsColors.bgSecondary)
                        .frame(width: 48, height: 48)

                    Image(systemName: viewModel.currentlyPlayingId == track.id ? "pause.fill" : "play.fill")
                        .font(.system(size: 18))
                        .foregroundColor(UsColors.postbookPrimary)
                }
            }
            .buttonStyle(.plain)

            // Title & Artist
            VStack(alignment: .leading, spacing: 2) {
                Text(track.title)
                    .font(.system(size: 15, weight: .semibold))
                    .foregroundColor(UsColors.textPrimary)
                Text("\(track.artist) • \(formatCount(track.usageCount)) reels")
                    .font(.system(size: 12))
                    .foregroundColor(UsColors.textMuted)
            }

            Spacer()

            // Use Sound Button
            Button(action: {
                viewModel.stop()
                onSelectTrack(track)
                onDismiss()
            }) {
                Text("Use Sound")
                    .font(.system(size: 13, weight: .bold))
                    .foregroundColor(.black)
                    .padding(.horizontal, 12)
                    .padding(.vertical, 6)
                    .background(Color.white)
                    .clipShape(Capsule())
            }
            .buttonStyle(.plain)
        }
        .padding(.vertical, 4)
    }

    private func formatCount(_ count: Int) -> String {
        if count >= 1_000 {
            return String(format: "%.1fK", Double(count) / 1_000)
        }
        return "\(count)"
    }
}
