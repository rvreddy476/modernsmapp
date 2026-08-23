import SwiftUI
import UsModel
import UsDesignSystem
import UsNetwork

@Observable
public final class DatingViewModel: @unchecked Sendable {
    public var profiles: [DatingProfile] = []
    public var matchedProfile: DatingProfile? = nil
    public var isLoading: Bool = false

    private let client: APIClientProtocol

    public init(client: APIClientProtocol = APIClient()) {
        self.client = client
        populateMockProfiles()
    }

    public func swipeRight(profile: DatingProfile) {
        HapticManager.shared.trigger(.success)
        profiles.removeAll { $0.id == profile.id }
        // Simulate 50% instant match
        if Bool.random() {
            matchedProfile = profile
        }
    }

    public func swipeLeft(profile: DatingProfile) {
        HapticManager.shared.trigger(.light)
        profiles.removeAll { $0.id == profile.id }
    }

    private func populateMockProfiles() {
        profiles = [
            DatingProfile(id: "dp1", name: "Aanya Sharma", age: 24, bio: "Architect by day, vinyl collector by night. Always looking for new coffee spots ☕️", occupation: "Architect", distanceKm: 3, photos: ["https://images.unsplash.com/photo-1534528741775-53994a69daeb?w=800"], interests: ["Architecture", "Vinyl", "Espresso", "Art"]),
            DatingProfile(id: "dp2", name: "Rohan Verma", age: 27, bio: "Building tech startups, training for half marathons, and obsessed with street food 🍜", occupation: "Product Founder", distanceKm: 5, photos: ["https://images.unsplash.com/photo-1507003211169-0a1dd7228f2d?w=800"], interests: ["Running", "Startups", "Street Food", "Film"]),
            DatingProfile(id: "dp3", name: "Meera Patel", age: 25, bio: "Curator & visual storyteller. Let's go gallery hopping!", occupation: "Gallery Curator", distanceKm: 2, photos: ["https://images.unsplash.com/photo-1517841905240-472988babdf9?w=800"], interests: ["Art Galleries", "Pottery", "Indie Music", "Books"])
        ]
    }
}

public struct DatingSwipeView: View {
    @State private var viewModel = DatingViewModel()
    @State private var offset: CGSize = .zero
    public let onStartChat: (Author) -> Void

    public init(onStartChat: @escaping (Author) -> Void = { _ in }) {
        self.onStartChat = onStartChat
    }

    public var body: some View {
        NavigationStack {
            ZStack {
                UsColors.bgPrimary
                    .ignoresSafeArea()

                if viewModel.profiles.isEmpty {
                    UsEmptyState(
                        title: "You're All Caught Up",
                        detail: "Check back later for new profiles nearby."
                    )
                } else {
                    VStack(spacing: 20) {
                        // Cards Stack
                        ZStack {
                            ForEach(viewModel.profiles.reversed()) { profile in
                                if profile.id == viewModel.profiles.first?.id {
                                    cardView(profile)
                                        .offset(x: offset.width, y: offset.height * 0.4)
                                        .rotationEffect(.degrees(Double(offset.width / 20)))
                                        .gesture(
                                            DragGesture()
                                                .onChanged { gesture in
                                                    offset = gesture.translation
                                                }
                                                .onEnded { gesture in
                                                    if gesture.translation.width > 120 {
                                                        viewModel.swipeRight(profile: profile)
                                                    } else if gesture.translation.width < -120 {
                                                        viewModel.swipeLeft(profile: profile)
                                                    }
                                                    offset = .zero
                                                }
                                        )
                                } else {
                                    cardView(profile)
                                        .scaleEffect(0.95)
                                }
                            }
                        }
                        .frame(maxHeight: .infinity)

                        // Action Buttons: Pass (X), Superlike (Star), Like (Heart)
                        HStack(spacing: 28) {
                            Button(action: {
                                if let first = viewModel.profiles.first {
                                    viewModel.swipeLeft(profile: first)
                                }
                            }) {
                                ZStack {
                                    Circle()
                                        .fill(UsColors.bgSecondary)
                                        .frame(width: 60, height: 60)
                                        .overlay(Circle().stroke(UsColors.borderMedium, lineWidth: 1))
                                    Image(systemName: "xmark")
                                        .font(.system(size: 24, weight: .bold))
                                        .foregroundColor(UsColors.statusError)
                                }
                            }

                            Button(action: {
                                if let first = viewModel.profiles.first {
                                    viewModel.swipeRight(profile: first)
                                }
                            }) {
                                ZStack {
                                    Circle()
                                        .fill(
                                            LinearGradient(
                                                colors: [UsColors.postgramPrimary, UsColors.postgramSecondary],
                                                startPoint: .topLeading,
                                                endPoint: .bottomTrailing
                                            )
                                        )
                                        .frame(width: 72, height: 72)
                                        .shadow(color: UsColors.postgramPrimary.opacity(0.4), radius: 12, x: 0, y: 6)
                                    Image(systemName: "heart.fill")
                                        .font(.system(size: 32, weight: .bold))
                                        .foregroundColor(.white)
                                }
                            }
                        }
                        .padding(.bottom, 24)
                    }
                    .padding(.horizontal, 16)
                }
            }
            .navigationTitle("Dating")
            .navigationBarTitleDisplayMode(.inline)
            .fullScreenCover(item: Binding(
                get: { viewModel.matchedProfile },
                set: { viewModel.matchedProfile = $0 }
            )) { match in
                matchPopup(match)
            }
        }
    }

    @ViewBuilder
    private func cardView(_ profile: DatingProfile) -> some View {
        ZStack(alignment: .bottomLeading) {
            // Profile Photo
            if let photo = profile.photos.first, let url = URL(string: photo) {
                AsyncImage(url: url) { phase in
                    switch phase {
                    case .success(let img):
                        img.resizable().scaledToFill()
                    default:
                        Rectangle().fill(UsColors.bgTertiary)
                    }
                }
                .frame(maxWidth: .infinity, maxHeight: .infinity)
                .clipped()
            }

            // Dark gradient overlay
            LinearGradient(
                colors: [Color.clear, Color.black.opacity(0.85)],
                startPoint: .center,
                endPoint: .bottom
            )

            // Bio & Details
            VStack(alignment: .leading, spacing: 8) {
                HStack(spacing: 8) {
                    Text("\(profile.name), \(profile.age)")
                        .font(.system(size: 24, weight: .bold))
                        .foregroundColor(.white)
                    Image(systemName: "checkmark.seal.fill")
                        .foregroundColor(UsColors.postbookPrimary)
                }

                Text("\(profile.occupation) • \(profile.distanceKm) km away")
                    .font(.system(size: 13))
                    .foregroundColor(.white.opacity(0.8))

                Text(profile.bio)
                    .font(.system(size: 14))
                    .foregroundColor(.white.opacity(0.9))
                    .lineLimit(3)

                // Interests Pills
                HStack(spacing: 6) {
                    ForEach(profile.interests, id: \.self) { interest in
                        Text(interest)
                            .font(.system(size: 11, weight: .medium))
                            .foregroundColor(.white)
                            .padding(.horizontal, 10)
                            .padding(.vertical, 4)
                            .background(Color.white.opacity(0.2))
                            .clipShape(Capsule())
                    }
                }
            }
            .padding(20)
        }
        .clipShape(RoundedRectangle(cornerRadius: 20))
        .shadow(radius: 10)
    }

    @ViewBuilder
    private func matchPopup(_ match: DatingProfile) -> some View {
        ZStack {
            Color.black.opacity(0.9).ignoresSafeArea()

            VStack(spacing: 24) {
                Text("It's a Match! 🎉")
                    .font(.system(size: 32, weight: .black, design: .rounded))
                    .foregroundColor(UsColors.postgramPrimary)

                Text("You and \(match.name) liked each other.")
                    .font(.system(size: 16))
                    .foregroundColor(.white.opacity(0.8))

                if let photo = match.photos.first, let url = URL(string: photo) {
                    AsyncImage(url: url) { phase in
                        switch phase {
                        case .success(let img):
                            img.resizable().scaledToFill()
                        default:
                            Circle().fill(UsColors.bgTertiary)
                        }
                    }
                    .frame(width: 140, height: 140)
                    .clipShape(Circle())
                    .overlay(Circle().stroke(UsColors.postgramPrimary, lineWidth: 4))
                }

                VStack(spacing: 12) {
                    Button(action: {
                        viewModel.matchedProfile = nil
                        onStartChat(Author(id: match.id, username: match.name.lowercased(), displayName: match.name))
                    }) {
                        Text("Send a Message")
                            .font(.system(size: 16, weight: .bold))
                            .foregroundColor(.black)
                            .frame(maxWidth: .infinity)
                            .padding(.vertical, 16)
                            .background(Color.white)
                            .clipShape(RoundedRectangle(cornerRadius: 14))
                    }

                    Button(action: { viewModel.matchedProfile = nil }) {
                        Text("Keep Swiping")
                            .font(.system(size: 15, weight: .semibold))
                            .foregroundColor(.white.opacity(0.8))
                    }
                }
                .padding(.horizontal, 32)
                .padding(.top, 16)
            }
            .padding(24)
        }
    }
}
