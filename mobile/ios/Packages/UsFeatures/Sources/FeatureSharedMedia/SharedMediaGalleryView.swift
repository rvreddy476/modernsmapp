import SwiftUI
import UsModel
import UsDesignSystem

public enum SharedMediaTab: String, CaseIterable, Identifiable {
    case media = "Media"
    case files = "Docs"
    case links = "Links"
    case audio = "Voice"

    public var id: String { rawValue }
}

public struct SharedMediaGalleryView: View {
    public let chatTitle: String
    public let onDismiss: () -> Void

    @State private var selectedTab: SharedMediaTab = .media

    public init(
        chatTitle: String = "Bangalore Builders Group 🚀",
        onDismiss: @escaping () -> Void = {}
    ) {
        self.chatTitle = chatTitle
        self.onDismiss = onDismiss
    }

    public var body: some View {
        NavigationStack {
            ZStack {
                UsColors.bgPrimary
                    .ignoresSafeArea()

                VStack(spacing: 16) {
                    // Segmented Filter
                    HStack(spacing: 8) {
                        ForEach(SharedMediaTab.allCases) { tab in
                            let isSelected = selectedTab == tab
                            Button(action: {
                                selectedTab = tab
                                HapticManager.shared.trigger(.selection)
                            }) {
                                Text(tab.rawValue)
                                    .font(.system(size: 13, weight: .bold))
                                    .foregroundColor(isSelected ? .black : UsColors.textPrimary)
                                    .padding(.horizontal, 16)
                                    .padding(.vertical, 8)
                                    .background(isSelected ? Color.white : UsColors.bgSecondary)
                                    .clipShape(Capsule())
                            }
                            .buttonStyle(.plain)
                        }
                    }
                    .padding(.horizontal, 16)
                    .padding(.top, 8)

                    // Tab Content
                    ScrollView {
                        switch selectedTab {
                        case .media:
                            mediaGrid
                        case .files:
                            docsList
                        case .links:
                            linksList
                        case .audio:
                            voiceList
                        }
                    }
                    .padding(.horizontal, 16)
                }
            }
            .navigationTitle("Shared Content")
            .navigationBarTitleDisplayMode(.inline)
            .toolbar {
                ToolbarItem(placement: .cancellationAction) {
                    Button("Close", action: onDismiss)
                        .foregroundColor(UsColors.textMuted)
                }
            }
        }
    }

    private var mediaGrid: some View {
        LazyVGrid(columns: [GridItem(.flexible()), GridItem(.flexible()), GridItem(.flexible())], spacing: 6) {
            ForEach(0..<12, id: \.self) { idx in
                RoundedRectangle(cornerRadius: 8)
                    .fill(Color.gray.opacity(0.2))
                    .aspectRatio(1, contentMode: .fit)
                    .overlay(
                        Image(systemName: "photo")
                            .font(.system(size: 20))
                            .foregroundColor(UsColors.textMuted)
                    )
            }
        }
    }

    private var docsList: some View {
        LazyVStack(spacing: 10) {
            docRow("App_Architecture_v2.pdf", size: "4.2 MB", date: "Aug 18, 2026")
            docRow("API_Specifications_Go.pdf", size: "1.8 MB", date: "Aug 14, 2026")
            docRow("Sprint_Deliverables.docx", size: "840 KB", date: "Aug 10, 2026")
        }
    }

    private var linksList: some View {
        LazyVStack(spacing: 10) {
            linkRow("https://github.com/modernsmapp/social", title: "Modern Super-App Monorepo")
            linkRow("https://us.app/creator/sarah", title: "Sarah's Creator Hub")
        }
    }

    private var voiceList: some View {
        LazyVStack(spacing: 10) {
            voiceRow("Voice Note (0:45)", sender: "Alex Rivera", date: "Yesterday")
            voiceRow("Voice Note (1:20)", sender: "Sarah Chen", date: "Aug 18")
        }
    }

    @ViewBuilder
    private func docRow(_ name: String, size: String, date: String) -> some View {
        HStack(spacing: 12) {
            Image(systemName: "doc.fill")
                .font(.system(size: 20))
                .foregroundColor(UsColors.postbookPrimary)

            VStack(alignment: .leading, spacing: 2) {
                Text(name)
                    .font(.system(size: 13, weight: .semibold))
                    .foregroundColor(UsColors.textPrimary)
                Text("\(size) • \(date)")
                    .font(.system(size: 11))
                    .foregroundColor(UsColors.textMuted)
            }

            Spacer()
        }
        .padding(12)
        .background(UsColors.bgSecondary)
        .clipShape(RoundedRectangle(cornerRadius: 12))
    }

    @ViewBuilder
    private func linkRow(_ url: String, title: String) -> some View {
        VStack(alignment: .leading, spacing: 4) {
            Text(title)
                .font(.system(size: 13, weight: .bold))
                .foregroundColor(UsColors.textPrimary)
            Text(url)
                .font(.system(size: 11))
                .foregroundColor(UsColors.postbookPrimary)
                .lineLimit(1)
        }
        .padding(12)
        .frame(maxWidth: .infinity, alignment: .leading)
        .background(UsColors.bgSecondary)
        .clipShape(RoundedRectangle(cornerRadius: 12))
    }

    @ViewBuilder
    private func voiceRow(_ title: String, sender: String, date: String) -> some View {
        HStack(spacing: 12) {
            Image(systemName: "waveform")
                .font(.system(size: 18))
                .foregroundColor(UsColors.onlineGreen)

            VStack(alignment: .leading, spacing: 2) {
                Text(title)
                    .font(.system(size: 13, weight: .semibold))
                    .foregroundColor(UsColors.textPrimary)
                Text("Sent by \(sender) • \(date)")
                    .font(.system(size: 11))
                    .foregroundColor(UsColors.textMuted)
            }

            Spacer()
        }
        .padding(12)
        .background(UsColors.bgSecondary)
        .clipShape(RoundedRectangle(cornerRadius: 12))
    }
}
