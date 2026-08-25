import SwiftUI
import UsModel
import UsDesignSystem

public struct TopFanMember: Identifiable {
    public let id: String
    public let rank: Int
    public let name: String
    public let pointsText: String
    public let badgeTier: String

    public init(id: String, rank: Int, name: String, pointsText: String, badgeTier: String) {
        self.id = id
        self.rank = rank
        self.name = name
        self.pointsText = pointsText
        self.badgeTier = badgeTier
    }
}

public struct FanLeaderboardView: View {
    public let creatorName: String
    public let onDismiss: () -> Void

    @State private var topFans: [TopFanMember] = [
        TopFanMember(id: "fan-1", rank: 1, name: "Marcus Vance", pointsText: "14,850 pts", badgeTier: "Diamond VIP 💎"),
        TopFanMember(id: "fan-2", rank: 2, name: "Aanya Sharma", pointsText: "11,200 pts", badgeTier: "Gold Supporter 🥇"),
        TopFanMember(id: "fan-3", rank: 3, name: "Kavya Patel", pointsText: "8,940 pts", badgeTier: "Silver Fan 🥈"),
        TopFanMember(id: "fan-4", rank: 4, name: "Rohan Nair", pointsText: "6,410 pts", badgeTier: "Bronze 🥉")
    ]

    public init(
        creatorName: String = "Sarah Chen",
        onDismiss: @escaping () -> Void = {}
    ) {
        self.creatorName = creatorName
        self.onDismiss = onDismiss
    }

    public var body: some View {
        NavigationStack {
            ZStack {
                UsColors.bgPrimary
                    .ignoresSafeArea()

                ScrollView {
                    VStack(alignment: .leading, spacing: 18) {
                        // Top Fan Podium Header
                        podiumHeader

                        Text("Monthly Leaderboard Rankings")
                            .font(.system(size: 15, weight: .bold))
                            .foregroundColor(UsColors.textPrimary)

                        LazyVStack(spacing: 10) {
                            ForEach(topFans) { fan in
                                fanRow(fan)
                            }
                        }
                    }
                    .padding(16)
                }
            }
            .navigationTitle("Top Supporters")
            .navigationBarTitleDisplayMode(.inline)
            .toolbar {
                ToolbarItem(placement: .cancellationAction) {
                    Button("Close", action: onDismiss)
                        .foregroundColor(UsColors.textMuted)
                }
            }
        }
    }

    private var podiumHeader: some View {
        VStack(spacing: 12) {
            Text("Top Supporter of August 🏆")
                .font(.system(size: 12, weight: .bold))
                .foregroundColor(Color.yellow)

            UsAvatar(name: topFans.first?.name ?? "Marcus", size: .large)
                .overlay(Circle().stroke(Color.yellow, lineWidth: 3))

            VStack(spacing: 2) {
                Text(topFans.first?.name ?? "")
                    .font(.system(size: 16, weight: .bold))
                    .foregroundColor(UsColors.textPrimary)
                Text(topFans.first?.pointsText ?? "")
                    .font(.system(size: 13, weight: .black, design: .rounded))
                    .foregroundColor(UsColors.onlineGreen)
            }
        }
        .padding(18)
        .frame(maxWidth: .infinity)
        .background(UsColors.bgSecondary)
        .clipShape(RoundedRectangle(cornerRadius: 18))
    }

    @ViewBuilder
    private func fanRow(_ fan: TopFanMember) -> some View {
        HStack(spacing: 12) {
            Text("#\(fan.rank)")
                .font(.system(size: 14, weight: .black, design: .rounded))
                .foregroundColor(fan.rank == 1 ? Color.yellow : (fan.rank == 2 ? Color.gray : UsColors.textMuted))
                .frame(width: 28)

            UsAvatar(name: fan.name, size: .small)

            VStack(alignment: .leading, spacing: 2) {
                Text(fan.name)
                    .font(.system(size: 13, weight: .semibold))
                    .foregroundColor(UsColors.textPrimary)
                Text(fan.badgeTier)
                    .font(.system(size: 10))
                    .foregroundColor(UsColors.postbookPrimary)
            }

            Spacer()

            Text(fan.pointsText)
                .font(.system(size: 13, weight: .bold, design: .monospaced))
                .foregroundColor(UsColors.onlineGreen)
        }
        .padding(12)
        .background(UsColors.bgSecondary)
        .clipShape(RoundedRectangle(cornerRadius: 12))
    }
}
