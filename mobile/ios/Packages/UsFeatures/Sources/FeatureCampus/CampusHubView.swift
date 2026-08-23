import SwiftUI
import UsModel
import UsDesignSystem

public struct CampusNoticeItem: Identifiable {
    public let id: String
    public let title: String
    public let clubName: String
    public let dateString: String
    public let category: String

    public init(id: String, title: String, clubName: String, dateString: String, category: String) {
        self.id = id
        self.title = title
        self.clubName = clubName
        self.dateString = dateString
        self.category = category
    }
}

public struct CampusHubView: View {
    public let onDismiss: () -> Void

    @State private var collegeName: String = "IIT Bombay Campus Hub 🎓"
    @State private var notices: [CampusNoticeItem] = [
        CampusNoticeItem(id: "cn-1", title: "Mood Indigo 2026 Core Team Auditions", clubName: "Cultural Council", dateString: "Today, 6:00 PM", category: "Festival"),
        CampusNoticeItem(id: "cn-2", title: "AI Hackathon: ₹5,00,000 Prize Pool", clubName: "Web & Coding Club", dateString: "Aug 28, 2026", category: "Tech"),
        CampusNoticeItem(id: "cn-3", title: "Selling: Engineering Mechanics (Timoshenko) Textbook", clubName: "Hostel 14 Flea", dateString: "Yesterday", category: "Flea Market")
    ]

    public init(onDismiss: @escaping () -> Void = {}) {
        self.onDismiss = onDismiss
    }

    public var body: some View {
        NavigationStack {
            ZStack {
                UsColors.bgPrimary
                    .ignoresSafeArea()

                ScrollView {
                    VStack(alignment: .leading, spacing: 18) {
                        // Campus Verification Badge Banner
                        HStack(spacing: 12) {
                            ZStack {
                                Circle().fill(UsColors.postbookPrimary.opacity(0.2)).frame(width: 44, height: 44)
                                Image(systemName: "graduationcap.fill")
                                    .foregroundColor(UsColors.postbookPrimary)
                                    .font(.system(size: 20))
                            }

                            VStack(alignment: .leading, spacing: 2) {
                                Text("Verified Student Network")
                                    .font(.system(size: 15, weight: .bold))
                                    .foregroundColor(UsColors.textPrimary)
                                Text("rollno@iitb.ac.in • Roll #22B0941")
                                    .font(.system(size: 11))
                                    .foregroundColor(UsColors.textMuted)
                            }

                            Spacer()

                            Text("Active")
                                .font(.system(size: 11, weight: .bold))
                                .foregroundColor(UsColors.onlineGreen)
                                .padding(.horizontal, 8)
                                .padding(.vertical, 4)
                                .background(UsColors.onlineGreen.opacity(0.15))
                                .clipShape(Capsule())
                        }
                        .padding(14)
                        .background(UsColors.bgSecondary)
                        .clipShape(RoundedRectangle(cornerRadius: 16))

                        Text("Campus Notice Board & Events")
                            .font(.system(size: 15, weight: .bold))
                            .foregroundColor(UsColors.textPrimary)

                        LazyVStack(spacing: 12) {
                            ForEach(notices) { notice in
                                noticeCard(notice)
                            }
                        }
                    }
                    .padding(16)
                }
            }
            .navigationTitle("Campus Hub")
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
    private func noticeCard(_ notice: CampusNoticeItem) -> some View {
        VStack(alignment: .leading, spacing: 8) {
            HStack {
                Text(notice.category.uppercased())
                    .font(.system(size: 9, weight: .black))
                    .foregroundColor(UsColors.postbookPrimary)
                    .padding(.horizontal, 6)
                    .padding(.vertical, 3)
                    .background(UsColors.bgTertiary)
                    .clipShape(RoundedRectangle(cornerRadius: 6))

                Spacer()

                Text(notice.dateString)
                    .font(.system(size: 11))
                    .foregroundColor(UsColors.textMuted)
            }

            Text(notice.title)
                .font(.system(size: 14, weight: .bold))
                .foregroundColor(UsColors.textPrimary)

            Text("Posted by \(notice.clubName)")
                .font(.system(size: 11))
                .foregroundColor(UsColors.textMuted)
        }
        .padding(14)
        .background(UsColors.bgSecondary)
        .clipShape(RoundedRectangle(cornerRadius: 14))
    }
}
