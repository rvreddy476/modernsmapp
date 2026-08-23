import SwiftUI
import PhotosUI
import UsModel
import UsDesignSystem
import UsNetwork
import UsMedia

public struct CreatePostPayload: Codable, Sendable {
    public struct Distribution: Codable, Sendable {
        public let version: Int
        public let mainFeed: Bool
        public let notifySubscribers: Bool
        public let createReelPreview: Bool

        enum CodingKeys: String, CodingKey {
            case version
            case mainFeed = "main_feed"
            case notifySubscribers = "notify_subscribers"
            case createReelPreview = "create_reel_preview"
        }

        public init(version: Int = 1, mainFeed: Bool = true, notifySubscribers: Bool = false, createReelPreview: Bool = false) {
            self.version = version
            self.mainFeed = mainFeed
            self.notifySubscribers = notifySubscribers
            self.createReelPreview = createReelPreview
        }
    }

    public let text: String
    public let visibility: String
    public let contentType: String
    public let postType: String
    public let appOrigin: String
    public let mediaIds: [String]
    public let language: String
    public let distribution: Distribution

    enum CodingKeys: String, CodingKey {
        case text
        case visibility
        case contentType = "content_type"
        case postType = "post_type"
        case appOrigin = "app_origin"
        case mediaIds = "media_ids"
        case language
        case distribution
    }

    public init(
        text: String,
        visibility: String = "public",
        contentType: String = "post",
        postType: String = "text",
        appOrigin: String = "postbook",
        mediaIds: [String] = [],
        language: String = "en",
        distribution: Distribution = Distribution()
    ) {
        self.text = text
        self.visibility = visibility
        self.contentType = contentType
        self.postType = postType
        self.appOrigin = appOrigin
        self.mediaIds = mediaIds
        self.language = language
        self.distribution = distribution
    }
}

public struct CreatedPostResponse: Codable, Sendable {
    public let id: String
    public let text: String
    public let visibility: String?
    public let postType: String?

    enum CodingKeys: String, CodingKey {
        case id
        case text
        case visibility
        case postType = "post_type"
    }
}

@Observable
public final class CreatePostViewModel: @unchecked Sendable {
    public var text: String = "" {
        didSet {
            if oldValue != text { persistDraft() }
        }
    }
    public var selectedPhotoItem: PhotosPickerItem? = nil
    public var selectedImageData: Data? = nil {
        didSet {
            if oldValue != selectedImageData { persistDraft() }
        }
    }
    public var selectedImageMimeType: String = "image/jpeg"
    public var altText: String = "" {
        didSet {
            if oldValue != altText { persistDraft() }
        }
    }
    public var isDecorative: Bool = false {
        didSet {
            if oldValue != isDecorative { persistDraft() }
        }
    }
    public var language: String = "en" {
        didSet {
            if oldValue != language { persistDraft() }
        }
    }

    public var idempotencyKey: String
    public var confirmedMediaId: String? = nil {
        didSet {
            if oldValue != confirmedMediaId { persistDraft() }
        }
    }
    public var uploadProgress: Double = 0.0
    public var isPublishing: Bool = false
    public var errorMessage: String? = nil
    public var isSuccess: Bool = false
    public var createdPostId: String? = nil

    private let client: APIClientProtocol
    private let mediaUploader: MediaUploaderProtocol
    private let draftStore: ComposerDraftStoreProtocol

    public init(
        client: APIClientProtocol = APIClient(),
        mediaUploader: MediaUploaderProtocol = MediaUploader(),
        draftStore: ComposerDraftStoreProtocol = FileComposerDraftStore.shared
    ) {
        self.client = client
        self.mediaUploader = mediaUploader
        self.draftStore = draftStore

        if let draft = draftStore.load(), draft.isValidForRestoration {
            self.idempotencyKey = draft.idempotencyKey
            self.text = draft.text
            self.confirmedMediaId = draft.confirmedMediaId
            self.altText = draft.altText
            self.isDecorative = draft.isDecorative
            self.language = draft.language
            self.selectedImageData = draft.selectedImageData
            self.selectedImageMimeType = draft.selectedImageMimeType
        } else {
            self.idempotencyKey = UUID().uuidString
        }
        // Always restored into an EDITING state, never into "publishing"
        self.isPublishing = false
    }

    public var isTextOverLimit: Bool {
        text.count > 5000
    }

    public var isAccessibilityValid: Bool {
        if selectedImageData == nil && confirmedMediaId == nil { return true }
        return !altText.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty || isDecorative
    }

    public var canPublish: Bool {
        let cleanText = text.trimmingCharacters(in: .whitespacesAndNewlines)
        let hasContent = !cleanText.isEmpty || selectedImageData != nil || confirmedMediaId != nil
        return hasContent && !isTextOverLimit && isAccessibilityValid && !isPublishing
    }

    public func persistDraft() {
        let cleanText = text.trimmingCharacters(in: .whitespacesAndNewlines)
        let hasContent = !cleanText.isEmpty || selectedImageData != nil || confirmedMediaId != nil
        if hasContent {
            let draft = ComposerDraft(
                idempotencyKey: idempotencyKey,
                text: text,
                confirmedMediaId: confirmedMediaId,
                altText: altText,
                isDecorative: isDecorative,
                language: language,
                selectedImageData: selectedImageData,
                selectedImageMimeType: selectedImageMimeType
            )
            draftStore.save(draft: draft)
        } else {
            draftStore.clear()
        }
    }

    public func discardDraft() {
        draftStore.clear()
        self.text = ""
        self.selectedImageData = nil
        self.selectedPhotoItem = nil
        self.confirmedMediaId = nil
        self.altText = ""
        self.isDecorative = false
        self.idempotencyKey = UUID().uuidString
        self.errorMessage = nil
    }

    @MainActor
    public func onPhotoSelected(_ item: PhotosPickerItem?) async {
        guard let item = item else {
            selectedImageData = nil
            confirmedMediaId = nil
            return
        }
        if let data = try? await item.loadTransferable(type: Data.self) {
            self.selectedImageData = data
            self.confirmedMediaId = nil
            // Sniff PNG vs JPEG
            if data.count >= 8 && data.prefix(8) == Data([0x89, 0x50, 0x4E, 0x47, 0x0D, 0x0A, 0x1A, 0x0A]) {
                self.selectedImageMimeType = "image/png"
            } else {
                self.selectedImageMimeType = "image/jpeg"
            }
            persistDraft()
        }
    }

    @MainActor
    public func publishPost() async {
        guard canPublish else { return }

        isPublishing = true
        errorMessage = nil
        uploadProgress = 0.0

        do {
            var mediaIds: [String] = []

            // 1. Upload media if present and not yet confirmed
            if let existingId = confirmedMediaId {
                mediaIds = [existingId]
            } else if let imageData = selectedImageData {
                let uploadedId = try await mediaUploader.uploadImage(
                    data: imageData,
                    mimeType: selectedImageMimeType,
                    altText: isDecorative ? "" : altText.trimmingCharacters(in: .whitespacesAndNewlines),
                    decorative: isDecorative,
                    uploadPurpose: "composer"
                ) { [weak self] progress in
                    Task { @MainActor in
                        self?.uploadProgress = progress
                    }
                }
                self.confirmedMediaId = uploadedId
                mediaIds = [uploadedId]
                persistDraft()
            }

            // 2. Create post with persistent frozen idempotency key
            let cleanText = text.trimmingCharacters(in: .whitespacesAndNewlines)
            let postType = mediaIds.isEmpty ? "text" : "image"

            let payload = CreatePostPayload(
                text: cleanText,
                visibility: "public",
                contentType: "post",
                postType: postType,
                appOrigin: "postbook",
                mediaIds: mediaIds,
                language: language,
                distribution: CreatePostPayload.Distribution(
                    version: 1,
                    mainFeed: true,
                    notifySubscribers: false,
                    createReelPreview: false
                )
            )

            let body = try JSONEncoder().encode(payload)
            let headers = ["Idempotency-Key": idempotencyKey]

            let response: CreatedPostResponse = try await client.request(
                endpoint: "v1/posts",
                method: "POST",
                query: nil,
                headers: headers,
                body: body
            )

            self.createdPostId = response.id
            self.isSuccess = true

            // Clear draft ONLY on success
            draftStore.clear()
        } catch let appError as AppError {
            self.errorMessage = appError.localizedDescription
            // Retain draft on failure so retry preserves the same key and draft bytes
        } catch {
            self.errorMessage = error.localizedDescription
            // Retain draft on failure so retry preserves the same key and draft bytes
        }
        self.isPublishing = false
    }
}

public struct CreatePostView: View {
    @State private var viewModel: CreatePostViewModel
    public let onDismiss: () -> Void

    public init(
        client: APIClientProtocol = APIClient(),
        mediaUploader: MediaUploaderProtocol = MediaUploader(),
        draftStore: ComposerDraftStoreProtocol = FileComposerDraftStore.shared,
        onDismiss: @escaping () -> Void = {}
    ) {
        _viewModel = State(initialValue: CreatePostViewModel(
            client: client,
            mediaUploader: mediaUploader,
            draftStore: draftStore
        ))
        self.onDismiss = onDismiss
    }

    public var body: some View {
        NavigationStack {
            ZStack {
                UsColors.bgPrimary
                    .ignoresSafeArea()

                ScrollView {
                    VStack(alignment: .leading, spacing: 16) {
                        // Audience & Language indicators (Truthful, non-interactive Public audience)
                        HStack(spacing: 12) {
                            HStack(spacing: 5) {
                                Image(systemName: "globe.americas.fill")
                                    .font(.system(size: 12))
                                Text("Audience: Public")
                                    .font(.system(size: 13, weight: .semibold))
                            }
                            .foregroundColor(UsColors.textSecondary)
                            .padding(.horizontal, 10)
                            .padding(.vertical, 6)
                            .background(UsColors.bgSecondary)
                            .clipShape(Capsule())
                            .accessibilityLabel("Audience is Public")

                            HStack(spacing: 5) {
                                Image(systemName: "character.bubble")
                                    .font(.system(size: 12))
                                Text("Language: English")
                                    .font(.system(size: 13, weight: .medium))
                            }
                            .foregroundColor(UsColors.textSecondary)
                            .padding(.horizontal, 10)
                            .padding(.vertical, 6)
                            .background(UsColors.bgSecondary)
                            .clipShape(Capsule())
                            .accessibilityLabel("Post language is English")

                            Spacer()
                        }
                        .padding(.top, 8)

                        // Text Composer Area
                        VStack(alignment: .trailing, spacing: 4) {
                            TextEditor(text: $viewModel.text)
                                .scrollContentBackground(.hidden)
                                .background(UsColors.bgSecondary)
                                .foregroundColor(UsColors.textPrimary)
                                .font(.system(size: 16))
                                .padding(12)
                                .clipShape(RoundedRectangle(cornerRadius: 12))
                                .frame(minHeight: 120)
                                .accessibilityLabel("Post content text field")

                            Text("\(viewModel.text.count)/5000")
                                .font(.system(size: 12))
                                .foregroundColor(viewModel.isTextOverLimit ? UsColors.statusError : UsColors.textMuted)
                        }

                        // Image preview & Accessibility Configuration
                        if let imgData = viewModel.selectedImageData,
                           let uiImage = UIImage(data: imgData) {
                            VStack(alignment: .leading, spacing: 12) {
                                ZStack(alignment: .topTrailing) {
                                    Image(uiImage: uiImage)
                                        .resizable()
                                        .scaledToFit()
                                        .frame(maxHeight: 200)
                                        .clipShape(RoundedRectangle(cornerRadius: 12))
                                        .accessibilityLabel("Selected image attachment")

                                    Button(action: {
                                        viewModel.selectedImageData = nil
                                        viewModel.selectedPhotoItem = nil
                                        viewModel.confirmedMediaId = nil
                                    }) {
                                        Image(systemName: "xmark.circle.fill")
                                            .font(.system(size: 24))
                                            .foregroundColor(.white)
                                            .padding(8)
                                    }
                                    .accessibilityLabel("Remove image attachment")
                                }

                                // Mandatory Accessibility Decision
                                VStack(alignment: .leading, spacing: 8) {
                                    Text("Image Accessibility (Required)")
                                        .font(.system(size: 13, weight: .bold))
                                        .foregroundColor(UsColors.textPrimary)

                                    if !viewModel.isDecorative {
                                        TextField("Add description for visually impaired users...", text: $viewModel.altText)
                                            .textFieldStyle(.plain)
                                            .font(.system(size: 14))
                                            .foregroundColor(UsColors.textPrimary)
                                            .padding(10)
                                            .background(UsColors.bgTertiary)
                                            .clipShape(RoundedRectangle(cornerRadius: 8))
                                            .accessibilityLabel("Image alt text description")
                                    }

                                    Toggle(isOn: $viewModel.isDecorative) {
                                        Text("Mark as decorative (no description needed)")
                                            .font(.system(size: 13))
                                            .foregroundColor(UsColors.textSecondary)
                                    }
                                    .tint(UsColors.postbookPrimary)
                                    .accessibilityLabel("Mark image as decorative")

                                    if !viewModel.isAccessibilityValid {
                                        Text("Please provide an image description or mark as decorative.")
                                            .font(.system(size: 12))
                                            .foregroundColor(UsColors.statusError)
                                    }
                                }
                                .padding(12)
                                .background(UsColors.bgSecondary)
                                .clipShape(RoundedRectangle(cornerRadius: 12))
                            }
                        }

                        // Upload progress indicator
                        if viewModel.isPublishing && viewModel.uploadProgress > 0 && viewModel.uploadProgress < 1.0 {
                            ProgressView(value: viewModel.uploadProgress, total: 1.0)
                                .tint(UsColors.postbookPrimary)
                                .padding(.vertical, 4)
                        }

                        // Media Attachment Toolbar
                        HStack(spacing: 12) {
                            PhotosPicker(
                                selection: $viewModel.selectedPhotoItem,
                                matching: .images
                            ) {
                                HStack(spacing: 6) {
                                    Image(systemName: "photo")
                                    Text(viewModel.selectedImageData == nil ? "Attach Photo" : "Change Photo")
                                }
                                .font(.system(size: 14, weight: .medium))
                                .foregroundColor(UsColors.postbookPrimary)
                                .padding(.horizontal, 14)
                                .padding(.vertical, 10)
                                .background(UsColors.bgSecondary)
                                .clipShape(Capsule())
                            }
                            .accessibilityLabel("Select photo from library")
                            .onChange(of: viewModel.selectedPhotoItem) { _, item in
                                Task { await viewModel.onPhotoSelected(item) }
                            }

                            Spacer()
                        }

                        if let err = viewModel.errorMessage {
                            Text(err)
                                .font(.system(size: 13, weight: .medium))
                                .foregroundColor(UsColors.statusError)
                                .padding(.top, 4)
                        }

                        Spacer()
                    }
                    .padding(16)
                }
            }
            .navigationTitle("New Post")
            .navigationBarTitleDisplayMode(.inline)
            .toolbar {
                ToolbarItem(placement: .cancellationAction) {
                    Button("Cancel") {
                        viewModel.discardDraft()
                        onDismiss()
                    }
                    .foregroundColor(UsColors.textMuted)
                    .accessibilityLabel("Cancel and discard post composition")
                }

                ToolbarItem(placement: .confirmationAction) {
                    Button(action: {
                        Task {
                            await viewModel.publishPost()
                            if viewModel.isSuccess {
                                ToastManager.shared.show("Post Published", style: .success)
                                onDismiss()
                            }
                        }
                    }) {
                        if viewModel.isPublishing {
                            ProgressView()
                                .tint(UsColors.postbookPrimary)
                        } else {
                            Text("Post")
                                .font(.system(size: 15, weight: .bold))
                                .foregroundColor(viewModel.canPublish ? UsColors.postbookPrimary : UsColors.textMuted)
                        }
                    }
                    .disabled(!viewModel.canPublish)
                    .accessibilityLabel("Publish post")
                }
            }
        }
    }
}
