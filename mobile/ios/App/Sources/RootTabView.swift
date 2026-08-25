import SwiftUI
import UsModel
import UsDesignSystem
import UsNetwork
import FeatureFeed
import FeatureReels
import FeaturePost
import FeatureProfile
import FeatureExplore
import FeatureChat
import FeatureCreate
import FeatureAuth
import FeatureStory
import FeatureNotifications
import FeatureSettings
import FeatureWatch
import FeatureLive
import FeatureWallet
import FeatureCommerce
import FeatureDating
import FeatureServicesHub
import FeatureFood
import FeatureRides
import FeatureBillPay
import FeatureCreatorStudio
import FeatureQRCode
import FeatureDigitalGold
import FeatureSplitBill
import FeatureAudioSpaces
import FeatureEvents
import FeatureMovies
import FeatureCarpool
import FeatureCoworking
import FeatureTransitStatus
import FeaturePetCare
import FeatureSports
import FeatureHomeServices

public struct RootTabView: View {
    @State private var selectedTab: Int = 0
    @State private var showCreateSheet: Bool = false
    @State private var showMessagesSheet: Bool = false
    @State private var showQRScannerSheet: Bool = false
    @State private var activeUserStories: UserStories? = nil
    @State private var navigationPath = NavigationPath()

    private let client: APIClientProtocol
    private let sessionManager: SessionManager

    // Mock/live stories list
    @State private var storiesList: [UserStories] = [
        UserStories(
            id: "user-1",
            author: Author(id: "user-1", username: "alex", displayName: "Alex Rivera"),
            stories: [
                StoryItem(
                    id: "s1",
                    authorId: "user-1",
                    mediaUrl: "https://images.unsplash.com/photo-1517841905240-472988babdf9?w=800",
                    mediaType: "image",
                    duration: 5.0,
                    createdAt: "2h"
                )
            ]
        ),
        UserStories(
            id: "user-2",
            author: Author(id: "user-2", username: "sarah", displayName: "Sarah Chen"),
            stories: [
                StoryItem(
                    id: "s2",
                    authorId: "user-2",
                    mediaUrl: "https://images.unsplash.com/photo-1534528741775-53994a69daeb?w=800",
                    mediaType: "image",
                    duration: 5.0,
                    createdAt: "4h"
                )
            ]
        ),
        UserStories(
            id: "user-3",
            author: Author(id: "user-3", username: "marcus", displayName: "Marcus Vance"),
            stories: [
                StoryItem(
                    id: "s3",
                    authorId: "user-3",
                    mediaUrl: "https://images.unsplash.com/photo-1507003211169-0a1dd7228f2d?w=800",
                    mediaType: "image",
                    duration: 5.0,
                    createdAt: "6h"
                )
            ]
        )
    ]

    public init(
        client: APIClientProtocol = APIClient(),
        sessionManager: SessionManager = .shared
    ) {
        self.client = client
        self.sessionManager = sessionManager
    }

    public var body: some View {
        Group {
            if sessionManager.isAuthenticated {
                authenticatedMainView
            } else {
                AuthView(client: client)
            }
        }
    }

    private var authenticatedMainView: some View {
        NavigationStack(path: $navigationPath) {
            ZStack(alignment: .bottom) {
                TabView(selection: $selectedTab) {
                    FeedView(
                        client: client,
                        onOpenPost: { postId in
                            navigationPath.append(AppRoute.postDetail(postId))
                        },
                        onOpenAuthor: { authorId in
                            navigationPath.append(AppRoute.userProfile(authorId))
                        },
                        onOpenNotifications: {
                            navigationPath.append(AppRoute.notifications)
                        },
                        onOpenChat: {
                            showMessagesSheet = true
                        },
                        onOpenScan: {
                            showQRScannerSheet = true
                        },
                        onOpenShortcut: { shortcutKey in
                            handleQuickShortcut(shortcutKey)
                        },
                        header: {
                            StoryTrayView(
                                userStories: storiesList,
                                currentUserId: sessionManager.currentSession?.userId ?? "me",
                                onSelectUserStories: { selected in
                                    activeUserStories = selected
                                },
                                onAddStory: {
                                    showCreateSheet = true
                                }
                            )
                        }
                    )
                    .tabItem {
                        UsIcons.home()
                        Text("Home")
                    }
                    .tag(0)

                    ExploreView(
                        client: client,
                        onOpenPost: { postId in
                            navigationPath.append(AppRoute.postDetail(postId))
                        },
                        onOpenAuthor: { authorId in
                            navigationPath.append(AppRoute.userProfile(authorId))
                        }
                    )
                    .tabItem {
                        UsIcons.explore()
                        Text("Explore")
                    }
                    .tag(1)

                    ReelsView(
                        client: client,
                        onOpenAuthor: { authorId in
                            navigationPath.append(AppRoute.userProfile(authorId))
                        }
                    )
                    .tabItem {
                        UsIcons.reels()
                        Text("Reels")
                    }
                    .tag(2)

                    ServicesHubView { service in
                        switch service {
                        case .wallet:
                            navigationPath.append(AppRoute.wallet)
                        case .shop:
                            navigationPath.append(AppRoute.shop)
                        case .dating:
                            navigationPath.append(AppRoute.dating)
                        case .watch:
                            navigationPath.append(AppRoute.watch("featured"))
                        case .food:
                            navigationPath.append(AppRoute.food)
                        case .rides:
                            navigationPath.append(AppRoute.rides)
                        case .bills:
                            navigationPath.append(AppRoute.bills)
                        case .live:
                            navigationPath.append(AppRoute.live("Creator Live Session", Author(id: "c1", username: "creator", displayName: "Lead Creator")))
                        }
                    }
                    .tabItem {
                        Image(systemName: "square.grid.2x2.fill")
                        Text("Services")
                    }
                    .tag(3)

                    ProfileView(
                        userId: sessionManager.currentSession?.userId ?? "me",
                        client: client,
                        onOpenPost: { postId in
                            navigationPath.append(AppRoute.postDetail(postId))
                        }
                    )
                    .tabItem {
                        UsIcons.profile()
                        Text("Profile")
                    }
                    .tag(4)
                }
                .tint(UsColors.postbookPrimary)
            }
            .navigationDestination(for: AppRoute.self) { route in
                switch route {
                case .postDetail(let postId):
                    PostDetailView(postId: postId, client: client) { authorId in
                        navigationPath.append(AppRoute.userProfile(authorId))
                    }
                case .userProfile(let userId):
                    ProfileView(userId: userId, client: client) { postId in
                        navigationPath.append(AppRoute.postDetail(postId))
                    }
                case .chat(let threadId, let participant):
                    ChatThreadView(threadId: threadId, participant: participant, client: client)
                case .settings:
                    SettingsView(sessionManager: sessionManager)
                case .watch(let postId):
                    WatchDetailView(
                        postId: postId,
                        client: client,
                        onOpenVideo: { nextId in
                            navigationPath.append(AppRoute.watch(nextId))
                        },
                        onOpenAuthor: { authorId in
                            navigationPath.append(AppRoute.userProfile(authorId))
                        }
                    )
                case .live(let streamTitle, let broadcaster):
                    LiveStreamView(
                        streamTitle: streamTitle,
                        broadcaster: broadcaster
                    ) {
                        navigationPath.removeLast()
                    }
                case .wallet:
                    WalletView(client: client)
                case .shop:
                    MarketplaceView()
                case .dating:
                    DatingSwipeView { author in
                        navigationPath.append(AppRoute.chat("match-\(author.id)", author))
                    }
                case .food:
                    FoodDeliveryView()
                case .rides:
                    RideBookingView {
                        navigationPath.removeLast()
                    }
                case .bills:
                    BillPayView {
                        navigationPath.removeLast()
                    }
                case .creatorStudio:
                    CreatorStudioView {
                        navigationPath.removeLast()
                    }
                case .notifications:
                    NotificationsView(
                        client: client,
                        onOpenPost: { postId in
                            navigationPath.append(AppRoute.postDetail(postId))
                        },
                        onOpenAuthor: { authorId in
                            navigationPath.append(AppRoute.userProfile(authorId))
                        }
                    )
                case .gold:
                    DigitalGoldView()
                case .split:
                    SplitBillView()
                case .events:
                    EventsDiscoveryView()
                case .spaces:
                    AudioSpacesView()
                case .movies:
                    MovieBookingView(client: client) {
                        navigationPath.removeLast()
                    }
                case .carpool:
                    CarpoolView(client: client) {
                        navigationPath.removeLast()
                    }
                case .coworking:
                    CoworkingBookingView(client: client) {
                        navigationPath.removeLast()
                    }
                case .transitStatus:
                    PNRTrackerView(client: client) {
                        navigationPath.removeLast()
                    }
                case .petCare:
                    PetCareServicesView(client: client) {
                        navigationPath.removeLast()
                    }
                case .sports:
                    TurfBookingView(client: client) {
                        navigationPath.removeLast()
                    }
                case .homeServices:
                    HomeServicesView(client: client) {
                        navigationPath.removeLast()
                    }
                }
            }
            .sheet(isPresented: $showCreateSheet) {
                CreatePostView(client: client) {
                    showCreateSheet = false
                }
            }
            .sheet(isPresented: $showMessagesSheet) {
                ChatListView(client: client)
            }
            .sheet(isPresented: $showQRScannerSheet) {
                QRCodeScannerView { scannedCode in
                    showQRScannerSheet = false
                    ToastManager.shared.show("Scanned QR Code: \(scannedCode)", style: .success)
                }
            }
            .fullScreenCover(item: Binding(
                get: { activeUserStories },
                set: { activeUserStories = $0 }
            )) { userStory in
                StoryViewerView(userStories: userStory, client: client) {
                    activeUserStories = nil
                }
            }
        }
    }

    private func handleQuickShortcut(_ key: String) {
        switch key {
        case "wallet":
            navigationPath.append(AppRoute.wallet)
        case "gold":
            navigationPath.append(AppRoute.gold)
        case "food":
            navigationPath.append(AppRoute.food)
        case "rides":
            navigationPath.append(AppRoute.rides)
        case "split":
            navigationPath.append(AppRoute.split)
        case "events":
            navigationPath.append(AppRoute.events)
        case "spaces":
            navigationPath.append(AppRoute.spaces)
        case "movies":
            navigationPath.append(AppRoute.movies)
        case "carpool":
            navigationPath.append(AppRoute.carpool)
        case "coworking":
            navigationPath.append(AppRoute.coworking)
        case "transit":
            navigationPath.append(AppRoute.transitStatus)
        case "petcare":
            navigationPath.append(AppRoute.petCare)
        case "sports":
            navigationPath.append(AppRoute.sports)
        case "homeservices":
            navigationPath.append(AppRoute.homeServices)
        default:
            break
        }
    }
}

public enum AppRoute: Hashable {
    case postDetail(String)
    case userProfile(String)
    case chat(String, Author)
    case settings
    case watch(String)
    case live(String, Author)
    case wallet
    case shop
    case dating
    case food
    case rides
    case bills
    case creatorStudio
    case notifications
    case gold
    case split
    case events
    case spaces
    case movies
    case carpool
    case coworking
    case transitStatus
    case petCare
    case sports
    case homeServices
}
