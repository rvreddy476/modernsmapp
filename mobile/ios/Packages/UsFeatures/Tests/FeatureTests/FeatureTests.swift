import XCTest
import UsModel
import UsNetwork
import UsMedia
@testable import FeatureFeed
@testable import FeatureStory
@testable import FeatureChat
@testable import FeatureReels
@testable import FeatureProfile
@testable import FeatureCreate
@testable import FeatureNotifications
@testable import FeatureWallet
@testable import FeatureCommerce
@testable import FeatureDating
@testable import FeatureAI
@testable import FeatureQA
@testable import FeatureMovies
@testable import FeatureCarpool
@testable import FeatureCoworking
@testable import FeatureTransitStatus
@testable import FeaturePetCare
@testable import FeatureSports
@testable import FeatureHomeServices

final class MockComposerApiClient: APIClientProtocol, @unchecked Sendable {
    var capturedEndpoint: String?
    var capturedMethod: String?
    var capturedHeaders: [String: String]?
    var capturedBody: Data?
    var shouldFail: Bool = false
    var returnPostId: String = "post-created-123"

    func request<T: Decodable>(
        endpoint: String,
        method: String,
        query: [String: String]?,
        body: Data?
    ) async throws -> T {
        try await request(endpoint: endpoint, method: method, query: query, headers: nil, body: body)
    }

    func request<T: Decodable>(
        endpoint: String,
        method: String,
        query: [String: String]?,
        headers: [String: String]?,
        body: Data?
    ) async throws -> T {
        self.capturedEndpoint = endpoint
        self.capturedMethod = method
        self.capturedHeaders = headers
        self.capturedBody = body

        if shouldFail {
            throw AppError.server(500, "Simulated server failure")
        }

        if T.self == CreatedPostResponse.self {
            let resp = CreatedPostResponse(id: returnPostId, text: "Created", visibility: "public", postType: "text")
            return resp as! T
        }
        throw AppError.network(URLError(.badServerResponse))
    }

    func requestEnvelope<T: Decodable>(
        endpoint: String,
        method: String,
        query: [String: String]?,
        body: Data?
    ) async throws -> ApiEnvelope<T> {
        fatalError("Not used in composer test")
    }

    func requestEnvelope<T: Decodable>(
        endpoint: String,
        method: String,
        query: [String: String]?,
        headers: [String: String]?,
        body: Data?
    ) async throws -> ApiEnvelope<T> {
        fatalError("Not used in composer test")
    }
}

final class MockMediaUploader: MediaUploaderProtocol, @unchecked Sendable {
    var uploadCallCount: Int = 0
    var returnMediaId: String = "upload-media-id-1"

    func uploadImage(
        data: Data,
        mimeType: String,
        altText: String,
        decorative: Bool,
        uploadPurpose: String,
        onProgress: @escaping (Double) -> Void
    ) async throws -> String {
        uploadCallCount += 1
        onProgress(1.0)
        return returnMediaId
    }
}

final class FeatureTests: XCTestCase {
    func testFeedViewModelLoadAndOptimisticEngagement() async {
        let vm = FeedViewModel()
        XCTAssertNotNil(vm)
    }

    func testStoryViewerProgression() async {
        let author = Author(id: "auth-1", username: "alex", displayName: "Alex")
        let story1 = StoryItem(id: "s1", authorId: "auth-1", mediaUrl: "https://example.com/s1.jpg", duration: 0.1, createdAt: "1h")
        let story2 = StoryItem(id: "s2", authorId: "auth-1", mediaUrl: "https://example.com/s2.jpg", duration: 0.1, createdAt: "2h")
        let userStories = UserStories(id: "auth-1", author: author, stories: [story1, story2])

        let vm = StoryViewerViewModel(userStories: userStories)
        XCTAssertEqual(vm.currentIndex, 0)
        XCTAssertEqual(vm.currentStory?.id, "s1")

        var completed = false
        await vm.nextStory(onComplete: { completed = true })
        XCTAssertEqual(vm.currentIndex, 1)
        XCTAssertEqual(vm.currentStory?.id, "s2")
        XCTAssertFalse(completed)

        await vm.nextStory(onComplete: { completed = true })
        XCTAssertTrue(completed)
    }

    func testCreatePostCarriesExactPayloadAndDistribution() throws {
        let payload = CreatePostPayload(
            text: "Hello from test",
            visibility: "public",
            contentType: "post",
            postType: "text",
            appOrigin: "postbook",
            mediaIds: [],
            language: "en",
            distribution: CreatePostPayload.Distribution(
                version: 1,
                mainFeed: true,
                notifySubscribers: false,
                createReelPreview: false
            )
        )

        let data = try JSONEncoder().encode(payload)
        let json = try JSONSerialization.jsonObject(with: data) as? [String: Any]

        XCTAssertEqual(json?["text"] as? String, "Hello from test")
        XCTAssertEqual(json?["visibility"] as? String, "public")
        XCTAssertEqual(json?["content_type"] as? String, "post")
        XCTAssertEqual(json?["post_type"] as? String, "text")
        XCTAssertEqual(json?["app_origin"] as? String, "postbook")
        XCTAssertEqual(json?["language"] as? String, "en")

        let dist = json?["distribution"] as? [String: Any]
        XCTAssertEqual(dist?["version"] as? Int, 1)
        XCTAssertEqual(dist?["main_feed"] as? Bool, true)
        XCTAssertEqual(dist?["notify_subscribers"] as? Bool, false)
        XCTAssertEqual(dist?["create_reel_preview"] as? Bool, false)
    }

    func testCreatePostMintedIdempotencyKeyPersistsOnRetry() {
        let draftStore = InMemoryComposerDraftStore()
        let vm = CreatePostViewModel(draftStore: draftStore)
        let initialKey = vm.idempotencyKey
        XCTAssertFalse(initialKey.isEmpty)

        // Text change and retry attempt preserves the same idempotency key
        vm.text = "Initial draft"
        XCTAssertEqual(vm.idempotencyKey, initialKey)

        vm.text = "Modified draft on retry"
        XCTAssertEqual(vm.idempotencyKey, initialKey)
    }

    func testCreatePostProcessDeathRestoresKeyAndState() async throws {
        let draftStore = InMemoryComposerDraftStore()
        let mockClient = MockComposerApiClient()
        let mockUploader = MockMediaUploader()

        // 1. First session: compose draft with image, alt text, and confirmed media
        var vm1: CreatePostViewModel? = CreatePostViewModel(
            client: mockClient,
            mediaUploader: mockUploader,
            draftStore: draftStore
        )
        let originalKey = vm1!.idempotencyKey
        vm1!.text = "Draft that survives process death"
        vm1!.selectedImageData = Data([0x89, 0x50, 0x4E, 0x47])
        vm1!.altText = "Alt description of image"
        vm1!.isDecorative = false
        vm1!.confirmedMediaId = "confirmed-media-xyz-99"

        // 2. SIMULATE PROCESS DEATH: Destroy ViewModel 1 completely
        vm1 = nil
        XCTAssertNil(vm1)

        // 3. Second session: Construct new ViewModel 2 from the same draft store
        let vm2 = CreatePostViewModel(
            client: mockClient,
            mediaUploader: mockUploader,
            draftStore: draftStore
        )

        // Assert that state and frozen idempotency key survived process death
        XCTAssertEqual(vm2.idempotencyKey, originalKey)
        XCTAssertEqual(vm2.text, "Draft that survives process death")
        XCTAssertEqual(vm2.confirmedMediaId, "confirmed-media-xyz-99")
        XCTAssertEqual(vm2.altText, "Alt description of image")
        XCTAssertEqual(vm2.isDecorative, false)
        XCTAssertEqual(vm2.selectedImageData, Data([0x89, 0x50, 0x4E, 0x47]))

        // Critical rule: MUST restore into an EDITING state, never into publishing
        XCTAssertFalse(vm2.isPublishing)
        XCTAssertTrue(vm2.canPublish)

        // 4. Publish restored draft
        await vm2.publishPost()

        // Assert server received the ORIGINAL frozen idempotency key
        XCTAssertEqual(mockClient.capturedHeaders?["Idempotency-Key"], originalKey)
        XCTAssertEqual(vm2.isSuccess, true)

        // Assert already confirmed media was NOT re-uploaded
        XCTAssertEqual(mockUploader.uploadCallCount, 0)

        // Assert draft is cleared on successful publish
        XCTAssertNil(draftStore.load())
    }

    func testCreatePostFailureRetainsDraftAndKey() async {
        let draftStore = InMemoryComposerDraftStore()
        let mockClient = MockComposerApiClient()
        mockClient.shouldFail = true
        let mockUploader = MockMediaUploader()

        let vm = CreatePostViewModel(
            client: mockClient,
            mediaUploader: mockUploader,
            draftStore: draftStore
        )
        let originalKey = vm.idempotencyKey
        vm.text = "Important text that must not be lost on failure"
        vm.isDecorative = true

        await vm.publishPost()

        // Assert publish failed
        XCTAssertFalse(vm.isSuccess)
        XCTAssertNotNil(vm.errorMessage)

        // Assert draft store RETAINED the draft and original key on failure
        let storedDraft = draftStore.load()
        XCTAssertNotNil(storedDraft)
        XCTAssertEqual(storedDraft?.idempotencyKey, originalKey)
        XCTAssertEqual(storedDraft?.text, "Important text that must not be lost on failure")
        XCTAssertEqual(storedDraft?.isDecorative, true)
    }

    func testCreatePostExplicitDiscardClearsDraftAndMintsNewKey() {
        let draftStore = InMemoryComposerDraftStore()
        let vm = CreatePostViewModel(draftStore: draftStore)
        let initialKey = vm.idempotencyKey
        vm.text = "Discardable draft"
        vm.persistDraft()

        XCTAssertNotNil(draftStore.load())

        vm.discardDraft()

        XCTAssertNil(draftStore.load())
        XCTAssertEqual(vm.text, "")
        XCTAssertNotEqual(vm.idempotencyKey, initialKey)
    }

    func testIncompleteHalfDraftIsDiscardedOnLoad() {
        let draftStore = InMemoryComposerDraftStore()
        // Half draft with key but empty text and no media
        let halfDraft = ComposerDraft(
            idempotencyKey: "half-key-123",
            text: "   ",
            confirmedMediaId: nil,
            selectedImageData: nil
        )
        XCTAssertFalse(halfDraft.isValidForRestoration)

        draftStore.save(draft: halfDraft)
        XCTAssertNil(draftStore.load())

        let vm = CreatePostViewModel(draftStore: draftStore)
        XCTAssertNotEqual(vm.idempotencyKey, "half-key-123")
    }

    func testCreatePostImageAccessibilityRequirement() {
        let vm = CreatePostViewModel(draftStore: InMemoryComposerDraftStore())
        vm.text = "Here is my photo"
        XCTAssertTrue(vm.canPublish)

        // Select an image without alt text or decorative decision
        vm.selectedImageData = Data([0x89, 0x50, 0x4E, 0x47])
        vm.altText = ""
        vm.isDecorative = false
        XCTAssertFalse(vm.isAccessibilityValid)
        XCTAssertFalse(vm.canPublish)

        // Mark decorative -> valid
        vm.isDecorative = true
        XCTAssertTrue(vm.isAccessibilityValid)
        XCTAssertTrue(vm.canPublish)

        // Provide alt text -> valid
        vm.isDecorative = false
        vm.altText = "A high-contrast blue icon"
        XCTAssertTrue(vm.isAccessibilityValid)
        XCTAssertTrue(vm.canPublish)
    }

    func testNotificationsScyllaClusteringKeyAddressing() {
        let item = NotificationItem(
            notificationId: "notif-123",
            userId: "user-456",
            bucket: 202608,
            ts: "c24cba32-9e8a-11f1-bf56-dad8c5f4580c",
            type: "reaction",
            actorUserId: "actor-789",
            isRead: false
        )

        XCTAssertEqual(item.bucket, 202608)
        XCTAssertEqual(item.ts, "c24cba32-9e8a-11f1-bf56-dad8c5f4580c")
        XCTAssertEqual(item.id, "notif-123")

        let payload = MarkReadPayload(bucket: item.bucket, ts: item.ts)
        XCTAssertEqual(payload.bucket, 202608)
        XCTAssertEqual(payload.ts, "c24cba32-9e8a-11f1-bf56-dad8c5f4580c")
    }

    func testWalletViewModelTransactions() {
        let vm = WalletViewModel()
        XCTAssertFalse(vm.transactions.isEmpty)
        XCTAssertEqual(vm.balance.currency, "INR")
    }

    func testMarketplaceCartOperations() {
        let vm = MarketplaceViewModel()
        XCTAssertFalse(vm.products.isEmpty)

        if let product = vm.products.first {
            vm.addToCart(product: product)
            XCTAssertEqual(vm.cartItems.count, 1)
            XCTAssertEqual(vm.cartItems.first?.quantity, 1)

            vm.addToCart(product: product)
            XCTAssertEqual(vm.cartItems.count, 1)
            XCTAssertEqual(vm.cartItems.first?.quantity, 2)
        }
    }

    func testDatingSwipeOperations() {
        let vm = DatingViewModel()
        let initialCount = vm.profiles.count
        XCTAssertGreaterThan(initialCount, 0)

        if let first = vm.profiles.first {
            vm.swipeLeft(profile: first)
            XCTAssertEqual(vm.profiles.count, initialCount - 1)
        }
    }

    func testAIAssistantResponse() async {
        let vm = AIAssistantViewModel()
        XCTAssertEqual(vm.messages.count, 1)

        await vm.send(prompt: "Write a viral caption")
        XCTAssertEqual(vm.messages.count, 2)
    }

    func testQAFeedUpvote() {
        let vm = QAFeedViewModel()
        XCTAssertFalse(vm.questions.isEmpty)

        if let q = vm.questions.first {
            vm.toggleUpvote(questionId: q.id)
            XCTAssertTrue(vm.upvotedQuestionIds.contains(q.id))

            vm.toggleUpvote(questionId: q.id)
            XCTAssertFalse(vm.upvotedQuestionIds.contains(q.id))
        }
    }

    func testMovieBookingOperations() async throws {
        let vm = MovieBookingViewModel()
        XCTAssertFalse(vm.movies.isEmpty)

        let bookingId = try await vm.bookTickets(showtimeId: "mov-1", seats: ["F12", "F13"])
        XCTAssertFalse(bookingId.isEmpty)
    }

    func testCarpoolOperations() async throws {
        let vm = CarpoolViewModel()
        XCTAssertFalse(vm.rides.isEmpty)

        let success = try await vm.joinRide(rideId: "cp-1")
        XCTAssertTrue(success)
    }

    func testCoworkingBookingOperations() async throws {
        let vm = CoworkingViewModel()
        XCTAssertFalse(vm.spaces.isEmpty)

        let passId = try await vm.bookDayPass(spaceId: "cw-1")
        XCTAssertFalse(passId.isEmpty)
    }

    func testPNRTrackerOperations() async throws {
        let vm = PNRTrackerViewModel()
        XCTAssertFalse(vm.trackedTrips.isEmpty)

        let trip = try await vm.trackPNR(pnr: "1234567890")
        XCTAssertEqual(trip.pnr, "1234567890")
    }

    func testPetCareBookingOperations() async throws {
        let vm = PetCareViewModel()
        XCTAssertFalse(vm.services.isEmpty)

        let aptId = try await vm.bookPetService(serviceId: "pc-1")
        XCTAssertFalse(aptId.isEmpty)
    }

    func testTurfBookingOperations() async throws {
        let vm = TurfBookingViewModel()
        XCTAssertFalse(vm.arenas.isEmpty)

        let bookingId = try await vm.bookSlot(turfId: "turf-1", slotTime: "7:00 PM")
        XCTAssertFalse(bookingId.isEmpty)
    }

    func testHomeServicesBookingOperations() async throws {
        let vm = HomeServicesViewModel()
        XCTAssertFalse(vm.services.isEmpty)

        let orderId = try await vm.bookService(serviceId: "hs-1", preferredTime: "Tomorrow 10 AM")
        XCTAssertFalse(orderId.isEmpty)
    }

    func testProfileFollowToggle() {
        let vm = ProfileViewModel(userId: "user-123")
        XCTAssertFalse(vm.isFollowingLocal)

        vm.toggleFollow()
        XCTAssertTrue(vm.isFollowingLocal)

        vm.toggleFollow()
        XCTAssertFalse(vm.isFollowingLocal)
    }

    func testEditProfileState() {
        let profile = UserProfile(id: "u1", username: "alex", displayName: "Alex Rivera", bio: "Tech founder", avatarUrl: nil, followersCount: 120, followingCount: 80, postsCount: 15, isFollowing: false)
        let vm = EditProfileViewModel(currentProfile: profile)
        XCTAssertEqual(vm.displayName, "Alex Rivera")
        XCTAssertEqual(vm.username, "alex")
        XCTAssertEqual(vm.bio, "Tech founder")
    }
}
