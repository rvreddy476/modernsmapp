import SwiftUI
import UsModel
import UsDesignSystem
import UsNetwork

public struct NearbyUser: Identifiable, Hashable {
    public let id: String
    public let name: String
    public let avatarUrl: String?
    public let distanceMeters: Int
    public let locationTag: String
    public let angleDegrees: Double
    public let radiusFactor: Double // 0.3 to 0.85

    public init(
        id: String,
        name: String,
        avatarUrl: String? = nil,
        distanceMeters: Int = 120,
        locationTag: String = "Third Wave Coffee",
        angleDegrees: Double = 45,
        radiusFactor: Double = 0.5
    ) {
        self.id = id
        self.name = name
        self.avatarUrl = avatarUrl
        self.distanceMeters = distanceMeters
        self.locationTag = locationTag
        self.angleDegrees = angleDegrees
        self.radiusFactor = radiusFactor
    }
}

public struct NearbyRadarView: View {
    public let onDismiss: () -> Void

    @State private var radarRotation: Double = 0
    @State private var selectedUser: NearbyUser? = nil

    private let nearbyUsers: [NearbyUser] = [
        NearbyUser(id: "n1", name: "Aanya", distanceMeters: 45, locationTag: "Third Wave Coffee ☕️", angleDegrees: 35, radiusFactor: 0.35),
        NearbyUser(id: "n2", name: "Rohan", distanceMeters: 120, locationTag: "Indiranagar Park 🌳", angleDegrees: 140, radiusFactor: 0.55),
        NearbyUser(id: "n3", name: "Sarah", distanceMeters: 280, locationTag: "Social Koramangala 🍕", angleDegrees: 230, radiusFactor: 0.75),
        NearbyUser(id: "n4", name: "Karan", distanceMeters: 340, locationTag: "Metro Station 🚇", angleDegrees: 310, radiusFactor: 0.85)
    ]

    public init(onDismiss: @escaping () -> Void = {}) {
        self.onDismiss = onDismiss
    }

    public var body: some View {
        NavigationStack {
            ZStack {
                UsColors.bgPrimary
                    .ignoresSafeArea()

                VStack(spacing: 24) {
                    Text("Discover Friends & Creators Nearby")
                        .font(.system(size: 14, weight: .semibold))
                        .foregroundColor(UsColors.textMuted)

                    // Radar Scope
                    radarScopeView
                        .frame(width: 320, height: 320)

                    // Selected user card or instructions
                    if let user = selectedUser {
                        selectedUserBanner(user)
                    } else {
                        Text("Tap any avatar on the radar to wave or start a chat")
                            .font(.system(size: 13))
                            .foregroundColor(UsColors.textDim)
                    }

                    Spacer()
                }
                .padding(16)
            }
            .navigationTitle("Nearby Radar")
            .navigationBarTitleDisplayMode(.inline)
            .toolbar {
                ToolbarItem(placement: .cancellationAction) {
                    Button("Close", action: onDismiss)
                        .foregroundColor(UsColors.textMuted)
                }
            }
            .onAppear {
                withAnimation(.linear(duration: 4.0).repeatForever(autoreverses: false)) {
                    radarRotation = 360
                }
            }
        }
    }

    private var radarScopeView: some View {
        ZStack {
            // Concentric Rings
            Circle().stroke(UsColors.postbookPrimary.opacity(0.1), lineWidth: 1).frame(width: 300, height: 300)
            Circle().stroke(UsColors.postbookPrimary.opacity(0.15), lineWidth: 1).frame(width: 220, height: 220)
            Circle().stroke(UsColors.postbookPrimary.opacity(0.2), lineWidth: 1).frame(width: 140, height: 140)
            Circle().stroke(UsColors.postbookPrimary.opacity(0.25), lineWidth: 1).frame(width: 60, height: 60)

            // Radar sweep gradient
            AngularGradient(
                gradient: Gradient(colors: [UsColors.postbookPrimary.opacity(0.4), .clear]),
                center: .center
            )
            .clipShape(Circle())
            .rotationEffect(.degrees(radarRotation))
            .frame(width: 300, height: 300)

            // Center User
            ZStack {
                Circle().fill(UsColors.postbookPrimary).frame(width: 24, height: 24)
                Circle().stroke(Color.white, lineWidth: 2).frame(width: 24, height: 24)
            }

            // Nearby Users Avatars positioned on radar
            ForEach(nearbyUsers) { user in
                let radians = user.angleDegrees * .pi / 180
                let r = 150.0 * user.radiusFactor
                let x = r * cos(radians)
                let y = r * sin(radians)

                Button(action: {
                    selectedUser = user
                    HapticManager.shared.trigger(.selection)
                }) {
                    VStack(spacing: 2) {
                        UsAvatar(name: user.name, url: user.avatarUrl, size: .small)
                            .overlay(Circle().stroke(selectedUser?.id == user.id ? Color.white : UsColors.postbookPrimary, lineWidth: 2))

                        Text(user.name)
                            .font(.system(size: 10, weight: .bold))
                            .foregroundColor(.white)
                            .padding(.horizontal, 4)
                            .background(Color.black.opacity(0.6))
                            .clipShape(Capsule())
                    }
                }
                .buttonStyle(.plain)
                .offset(x: x, y: y)
            }
        }
    }

    @ViewBuilder
    private func selectedUserBanner(_ user: NearbyUser) -> some View {
        HStack(spacing: 12) {
            UsAvatar(name: user.name, url: user.avatarUrl, size: .medium)

            VStack(alignment: .leading, spacing: 2) {
                Text(user.name)
                    .font(.system(size: 15, weight: .bold))
                    .foregroundColor(UsColors.textPrimary)
                Text("\(user.distanceMeters)m away • \(user.locationTag)")
                    .font(.system(size: 12))
                    .foregroundColor(UsColors.textMuted)
            }

            Spacer()

            Button(action: {
                HapticManager.shared.trigger(.success)
                ToastManager.shared.show("Waved 👋 to \(user.name)!", style: .success)
            }) {
                Text("Wave 👋")
                    .font(.system(size: 13, weight: .bold))
                    .foregroundColor(.black)
                    .padding(.horizontal, 16)
                    .padding(.vertical, 8)
                    .background(Color.white)
                    .clipShape(Capsule())
            }
        }
        .padding(14)
        .background(UsColors.bgSecondary)
        .clipShape(RoundedRectangle(cornerRadius: 16))
    }
}
