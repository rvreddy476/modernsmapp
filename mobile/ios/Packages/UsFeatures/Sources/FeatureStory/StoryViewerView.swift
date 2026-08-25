import SwiftUI
import UsModel
import UsDesignSystem
import UsNetwork

@Observable
public final class StoryViewerViewModel: @unchecked Sendable {
    public var userStories: UserStories
    public var currentIndex: Int = 0
    public var progress: Double = 0.0
    public var isPaused: Bool = false
    public var quickReplyText: String = ""

    private var timerTask: Task<Void, Never>?
    private let client: APIClientProtocol

    public init(userStories: UserStories, client: APIClientProtocol = APIClient()) {
        self.userStories = userStories
        self.client = client
    }

    public var currentStory: StoryItem? {
        guard currentIndex < userStories.stories.count else { return nil }
        return userStories.stories[currentIndex]
    }

    @MainActor
    public func startProgress(onComplete: @escaping () -> Void) {
        timerTask?.cancel()
        progress = 0.0

        timerTask = Task {
            let totalSteps = 100
            let interval = (currentStory?.duration ?? 5.0) / Double(totalSteps)

            for step in 1...totalSteps {
                guard !Task.isCancelled else { return }
                while isPaused {
                    try? await Task.sleep(nanoseconds: 100_000_000)
                }

                try? await Task.sleep(nanoseconds: UInt64(interval * 1_000_000_000))
                await MainActor.run {
                    self.progress = Double(step) / Double(totalSteps)
                }
            }

            await MainActor.run {
                self.nextStory(onComplete: onComplete)
            }
        }
    }

    @MainActor
    public func nextStory(onComplete: @escaping () -> Void) {
        timerTask?.cancel()
        if currentIndex + 1 < userStories.stories.count {
            currentIndex += 1
            startProgress(onComplete: onComplete)
        } else {
            onComplete()
        }
    }

    @MainActor
    public func previousStory(onComplete: @escaping () -> Void) {
        timerTask?.cancel()
        if currentIndex > 0 {
            currentIndex -= 1
            startProgress(onComplete: onComplete)
        } else {
            startProgress(onComplete: onComplete)
        }
    }

    public func pause() {
        isPaused = true
    }

    public func resume() {
        isPaused = false
    }

    public func cancel() {
        timerTask?.cancel()
    }
}

public struct StoryViewerView: View {
    @State private var viewModel: StoryViewerViewModel
    public let onDismiss: () -> Void

    public init(
        userStories: UserStories,
        client: APIClientProtocol = APIClient(),
        onDismiss: @escaping () -> Void
    ) {
        _viewModel = State(initialValue: StoryViewerViewModel(userStories: userStories, client: client))
        self.onDismiss = onDismiss
    }

    public var body: some View {
        ZStack {
            Color.black.ignoresSafeArea()

            // 1. Story Media Background
            if let story = viewModel.currentStory, let url = URL(string: story.mediaUrl) {
                AsyncImage(url: url) { phase in
                    switch phase {
                    case .success(let image):
                        image
                            .resizable()
                            .scaledToFit()
                            .frame(maxWidth: .infinity, maxHeight: .infinity)
                    default:
                        ZStack {
                            Color(red: 0x14/255.0, green: 0x14/255.0, blue: 0x1E/255.0)
                            ProgressView().tint(.white)
                        }
                    }
                }
                .ignoresSafeArea()
            }

            // 2. Gesture tap zones (Left = Prev, Right = Next, Hold = Pause)
            HStack(spacing: 0) {
                Color.clear
                    .contentShape(Rectangle())
                    .onTapGesture {
                        viewModel.previousStory(onComplete: onDismiss)
                    }

                Color.clear
                    .contentShape(Rectangle())
                    .onTapGesture {
                        viewModel.nextStory(onComplete: onDismiss)
                    }
            }
            .simultaneousGesture(
                DragGesture(minimumDistance: 0)
                    .onChanged { _ in viewModel.pause() }
                    .onEnded { _ in viewModel.resume() }
            )

            // 3. Top Overlay: Segmented Progress Bars + Creator Header + Close
            VStack {
                VStack(spacing: 8) {
                    // Segmented Progress Bar
                    HStack(spacing: 4) {
                        ForEach(0..<viewModel.userStories.stories.count, id: \.self) { idx in
                            GeometryReader { geo in
                                ZStack(alignment: .leading) {
                                    Capsule()
                                        .fill(Color.white.opacity(0.35))
                                    if idx < viewModel.currentIndex {
                                        Capsule().fill(Color.white)
                                    } else if idx == viewModel.currentIndex {
                                        Capsule()
                                            .fill(Color.white)
                                            .frame(width: geo.size.width * CGFloat(viewModel.progress))
                                    }
                                }
                            }
                            .frame(height: 3)
                        }
                    }
                    .padding(.horizontal, 16)
                    .padding(.top, 12)

                    // Creator info row
                    HStack(spacing: 10) {
                        UsAvatar(
                            name: viewModel.userStories.author.nameForDisplay,
                            url: viewModel.userStories.author.avatarUrl,
                            size: .small
                        )

                        Text(viewModel.userStories.author.nameForDisplay)
                            .font(.system(size: 14, weight: .bold))
                            .foregroundColor(.white)

                        if let story = viewModel.currentStory {
                            Text(story.createdAt)
                                .font(.system(size: 12))
                                .foregroundColor(.white.opacity(0.7))
                        }

                        Spacer()

                        Button(action: onDismiss) {
                            Image(systemName: "xmark")
                                .font(.system(size: 16, weight: .semibold))
                                .foregroundColor(.white)
                                .padding(8)
                        }
                    }
                    .padding(.horizontal, 16)
                }

                Spacer()

                // Quick reply bar at bottom
                HStack(spacing: 12) {
                    TextField("Send message...", text: $viewModel.quickReplyText)
                        .textFieldStyle(.plain)
                        .font(.system(size: 14))
                        .foregroundColor(.white)
                        .padding(.horizontal, 16)
                        .padding(.vertical, 10)
                        .background(Color.white.opacity(0.2))
                        .clipShape(Capsule())
                        .overlay(Capsule().stroke(Color.white.opacity(0.3), lineWidth: 1))

                    Button(action: {
                        // Send quick heart reaction
                    }) {
                        Image(systemName: "heart.fill")
                            .font(.system(size: 24))
                            .foregroundColor(UsColors.postgramPrimary)
                    }

                    Button(action: {
                        // Quick share
                    }) {
                        Image(systemName: "paperplane.fill")
                            .font(.system(size: 22))
                            .foregroundColor(.white)
                    }
                }
                .padding(.horizontal, 16)
                .padding(.bottom, 24)
            }
        }
        .onAppear {
            viewModel.startProgress(onComplete: onDismiss)
        }
        .onDisappear {
            viewModel.cancel()
        }
    }
}
