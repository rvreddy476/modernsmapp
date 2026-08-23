import SwiftUI
import UsModel
import UsDesignSystem

public struct OfflineMediaItem: Identifiable {
    public let id: String
    public let title: String
    public let authorName: String
    public let fileSizeMB: Double
    public let durationString: String
    public let isVideo: Bool

    public init(id: String, title: String, authorName: String, fileSizeMB: Double, durationString: String, isVideo: Bool = true) {
        self.id = id
        self.title = title
        self.authorName = authorName
        self.fileSizeMB = fileSizeMB
        self.durationString = durationString
        self.isVideo = isVideo
    }
}

public struct OfflineVaultView: View {
    public let onDismiss: () -> Void

    @State private var items: [OfflineMediaItem] = [
        OfflineMediaItem(id: "off-1", title: "Building India's Super-App Architecture Keynote", authorName: "Sarah Chen", fileSizeMB: 48.2, durationString: "18:42"),
        OfflineMediaItem(id: "off-2", title: "Bangalore Indie Rock Sunset Acoustic Set", authorName: "Prateek Kuhad", fileSizeMB: 24.5, durationString: "06:15", isVideo: false),
        OfflineMediaItem(id: "off-3", title: "Top 10 Secret Street Food Spots in Delhi", authorName: "Alex Rivera", fileSizeMB: 38.0, durationString: "12:04")
    ]

    public init(onDismiss: @escaping () -> Void = {}) {
        self.onDismiss = onDismiss
    }

    private var totalUsedStorageMB: Double {
        items.reduce(0) { $0 + $1.fileSizeMB }
    }

    public var body: some View {
        NavigationStack {
            ZStack {
                UsColors.bgPrimary
                    .ignoresSafeArea()

                ScrollView {
                    VStack(alignment: .leading, spacing: 18) {
                        // Storage meter banner
                        storageBar

                        Text("Downloaded Items (\(items.count))")
                            .font(.system(size: 15, weight: .bold))
                            .foregroundColor(UsColors.textPrimary)

                        LazyVStack(spacing: 12) {
                            ForEach(items) { item in
                                offlineItemRow(item)
                            }
                        }
                    }
                    .padding(16)
                }
            }
            .navigationTitle("Offline Downloads")
            .navigationBarTitleDisplayMode(.inline)
            .toolbar {
                ToolbarItem(placement: .cancellationAction) {
                    Button("Close", action: onDismiss)
                        .foregroundColor(UsColors.textMuted)
                }
            }
        }
    }

    private var storageBar: some View {
        VStack(alignment: .leading, spacing: 8) {
            HStack {
                Text("Offline Storage Used")
                    .font(.system(size: 12))
                    .foregroundColor(UsColors.textMuted)

                Spacer()

                Text(String(format: "%.1f MB of 2.0 GB", totalUsedStorageMB))
                    .font(.system(size: 12, weight: .bold, design: .monospaced))
                    .foregroundColor(UsColors.postbookPrimary)
            }

            Capsule()
                .fill(UsColors.bgTertiary)
                .frame(height: 8)
                .overlay(
                    GeometryReader { geo in
                        Capsule()
                            .fill(UsColors.postbookPrimary)
                            .frame(width: max(8, geo.size.width * CGFloat(totalUsedStorageMB / 2000.0)), height: 8)
                    },
                    alignment: .leading
                )
        }
        .padding(14)
        .background(UsColors.bgSecondary)
        .clipShape(RoundedRectangle(cornerRadius: 14))
    }

    @ViewBuilder
    private func offlineItemRow(_ item: OfflineMediaItem) -> some View {
        HStack(spacing: 12) {
            ZStack {
                Circle()
                    .fill(UsColors.bgTertiary)
                    .frame(width: 44, height: 44)

                Image(systemName: item.isVideo ? "play.circle.fill" : "headphones")
                    .font(.system(size: 20))
                    .foregroundColor(UsColors.postbookPrimary)
            }

            VStack(alignment: .leading, spacing: 2) {
                Text(item.title)
                    .font(.system(size: 14, weight: .semibold))
                    .foregroundColor(UsColors.textPrimary)
                    .lineLimit(1)

                Text("\(item.authorName) • \(item.durationString) • \(String(format: "%.1f MB", item.fileSizeMB))")
                    .font(.system(size: 11))
                    .foregroundColor(UsColors.textMuted)
            }

            Spacer()

            Button(action: {
                HapticManager.shared.trigger(.selection)
                ToastManager.shared.show("Playing \(item.title) offline!", style: .success)
            }) {
                Image(systemName: "play.fill")
                    .font(.system(size: 13))
                    .foregroundColor(.black)
                    .padding(10)
                    .background(Color.white)
                    .clipShape(Circle())
            }
        }
        .padding(12)
        .background(UsColors.bgSecondary)
        .clipShape(RoundedRectangle(cornerRadius: 14))
    }
}
