import SwiftUI
import UsModel
import UsDesignSystem
import UsNetwork

public struct AIGenerateRequest: Codable, Sendable {
    public let prompt: String

    public init(prompt: String) {
        self.prompt = prompt
    }
}

public struct AIGenerateResponse: Codable, Sendable {
    public let reply: String
}

@Observable
public final class AIAssistantViewModel: @unchecked Sendable {
    public var messages: [AIMessage] = []
    public var draftInput: String = ""
    public var isThinking: Bool = false

    private let client: APIClientProtocol

    public let quickPrompts = [
        "✨ Write a viral Reel caption",
        "#️⃣ Trending hashtags for Bangalore",
        "🌐 Translate to Hindi (हिंदी)",
        "💡 Content ideas for Tech creator"
    ]

    public init(client: APIClientProtocol = APIClient()) {
        self.client = client
        self.messages = [
            AIMessage(role: "assistant", content: "Hi there! I'm US AI, your creative co-pilot. How can I help you create, translate, or brainstorm today?")
        ]
    }

    @MainActor
    public func send(prompt: String? = nil) {
        let textToSend = prompt ?? draftInput.trimmingCharacters(in: .whitespacesAndNewlines)
        guard !textToSend.isEmpty else { return }

        let userMsg = AIMessage(role: "user", content: textToSend)
        messages.append(userMsg)
        draftInput = ""
        isThinking = true

        Task {
            // Attempt live backend AI request to ai-service
            var responseText: String? = nil
            do {
                let body = try JSONEncoder().encode(AIGenerateRequest(prompt: textToSend))
                let res: AIGenerateResponse = try await client.request(
                    endpoint: "v1/ai/generate",
                    method: "POST",
                    query: nil,
                    body: body
                )
                responseText = res.reply
            } catch {
                // Fallback to local heuristic generator
                responseText = generateLocalResponse(for: textToSend)
            }

            let aiMsg = AIMessage(role: "assistant", content: responseText ?? "I'm here to help!")
            await MainActor.run {
                self.messages.append(aiMsg)
                self.isThinking = false
            }
        }
    }

    private func generateLocalResponse(for prompt: String) -> String {
        if prompt.contains("caption") {
            return "Here are 3 viral caption options:\n\n1. Building what's next, one line of code at a time 🚀 #BuildInPublic #TechIndia\n2. Sometimes the greatest adventures start with a single cup of coffee ☕️✨\n3. Proof that big dreams and late nights always pay off."
        } else if prompt.contains("hashtags") {
            return "Here are the top trending tags:\n#BangaloreDiaries #NammaBengaluru #TechLifestyle #CreatorCommunity #InstaIndia #ReelKaroFeelKaro"
        } else if prompt.contains("Translate") {
            return "हिंदी अनुवाद:\n\n\"आज का दिन एक नई शुरुआत है। अपने सपनों को सच करने के लिए कड़ी मेहनत करें। 🌟\""
        }
        return "That's an inspiring idea! You could structure this as a 3-part Reel series: Part 1 hooks the problem, Part 2 reveals the secret insight, and Part 3 calls viewers to share their thoughts in the comments."
    }
}

public struct AIAssistantView: View {
    @State private var viewModel: AIAssistantViewModel
    public let onDismiss: () -> Void

    public init(client: APIClientProtocol = APIClient(), onDismiss: @escaping () -> Void = {}) {
        _viewModel = State(initialValue: AIAssistantViewModel(client: client))
        self.onDismiss = onDismiss
    }

    public var body: some View {
        NavigationStack {
            ZStack {
                UsColors.bgPrimary
                    .ignoresSafeArea()

                VStack(spacing: 0) {
                    // Chat Messages
                    ScrollViewReader { proxy in
                        ScrollView {
                            LazyVStack(spacing: 16) {
                                ForEach(viewModel.messages) { msg in
                                    messageBubble(msg)
                                        .id(msg.id)
                                }

                                if viewModel.isThinking {
                                    HStack {
                                        ProgressView().tint(UsColors.postgramPrimary)
                                        Text("US AI is thinking...")
                                            .font(.system(size: 13))
                                            .foregroundColor(UsColors.textMuted)
                                        Spacer()
                                    }
                                    .padding(.horizontal, 16)
                                }
                            }
                            .padding(16)
                        }
                    }

                    // Quick Prompts Row
                    ScrollView(.horizontal, showsIndicators: false) {
                        HStack(spacing: 8) {
                            ForEach(viewModel.quickPrompts, id: \.self) { p in
                                Button(action: { viewModel.send(prompt: p) }) {
                                    Text(p)
                                        .font(.system(size: 12, weight: .medium))
                                        .foregroundColor(UsColors.textPrimary)
                                        .padding(.horizontal, 12)
                                        .padding(.vertical, 8)
                                        .background(UsColors.bgSecondary)
                                        .clipShape(Capsule())
                                        .overlay(Capsule().stroke(UsColors.borderMedium, lineWidth: 1))
                                }
                                .buttonStyle(.plain)
                            }
                        }
                        .padding(.horizontal, 16)
                        .padding(.vertical, 8)
                    }

                    // Input Bar
                    HStack(spacing: 10) {
                        TextField("Ask US AI anything...", text: $viewModel.draftInput)
                            .textFieldStyle(.plain)
                            .padding(12)
                            .background(UsColors.bgSecondary)
                            .clipShape(RoundedRectangle(cornerRadius: 12))
                            .foregroundColor(UsColors.textPrimary)
                            .onSubmit {
                                viewModel.send()
                            }

                        Button(action: { viewModel.send() }) {
                            ZStack {
                                Circle()
                                    .fill(
                                        LinearGradient(
                                            colors: [UsColors.postgramPrimary, UsColors.postgramSecondary],
                                            startPoint: .topLeading,
                                            endPoint: .bottomTrailing
                                        )
                                    )
                                    .frame(width: 44, height: 44)

                                Image(systemName: "arrow.up")
                                    .font(.system(size: 18, weight: .bold))
                                    .foregroundColor(.white)
                            }
                        }
                        .disabled(viewModel.draftInput.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty)
                    }
                    .padding(16)
                    .background(UsColors.bgSecondary.opacity(0.5))
                }
            }
            .navigationTitle("US AI Assistant")
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
    private func messageBubble(_ msg: AIMessage) -> some View {
        HStack(alignment: .top, spacing: 10) {
            if msg.role == "assistant" {
                ZStack {
                    Circle()
                        .fill(
                            LinearGradient(
                                colors: [UsColors.postgramPrimary, UsColors.postgramSecondary],
                                startPoint: .topLeading,
                                endPoint: .bottomTrailing
                            )
                        )
                        .frame(width: 32, height: 32)
                    Image(systemName: "sparkles")
                        .font(.system(size: 14))
                        .foregroundColor(.white)
                }
            } else {
                Spacer()
            }

            VStack(alignment: msg.role == "user" ? .trailing : .leading, spacing: 4) {
                Text(msg.content)
                    .font(.system(size: 14))
                    .foregroundColor(msg.role == "user" ? .black : UsColors.textPrimary)
                    .padding(.horizontal, 14)
                    .padding(.vertical, 10)
                    .background(msg.role == "user" ? Color.white : UsColors.bgSecondary)
                    .clipShape(RoundedRectangle(cornerRadius: 16))

                Text(msg.timestamp)
                    .font(.system(size: 10))
                    .foregroundColor(UsColors.textDim)
            }

            if msg.role == "user" {
                // User Avatar placeholder
            } else {
                Spacer()
            }
        }
    }
}
