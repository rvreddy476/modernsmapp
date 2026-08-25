import SwiftUI
import PhotosUI
import UsModel
import UsDesignSystem
import UsNetwork
import UsMedia

@Observable
public final class EditProfileViewModel: @unchecked Sendable {
    public var displayName: String = ""
    public var username: String = ""
    public var bio: String = ""
    public var website: String = ""
    public var location: String = ""

    public var selectedPhotoItem: PhotosPickerItem? = nil
    public var avatarData: Data? = nil
    public var currentAvatarUrl: String? = nil

    public var isSaving: Bool = false
    public var errorMessage: String? = nil
    public var isSuccess: Bool = false

    private let client: APIClientProtocol
    private let uploader: MediaUploaderProtocol

    public init(
        currentProfile: UserProfile? = nil,
        client: APIClientProtocol = APIClient(),
        uploader: MediaUploaderProtocol = MediaUploader()
    ) {
        self.client = client
        self.uploader = uploader
        if let p = currentProfile {
            self.displayName = p.displayName ?? ""
            self.username = p.username ?? ""
            self.bio = p.bio ?? ""
            self.currentAvatarUrl = p.avatarUrl
        }
    }

    @MainActor
    public func onPhotoSelected(_ item: PhotosPickerItem?) async {
        guard let item = item else { return }
        if let data = try? await item.loadTransferable(type: Data.self) {
            self.avatarData = data
        }
    }

    @MainActor
    public func saveProfile() async {
        isSaving = true
        errorMessage = nil

        var uploadedAvatarUrl = currentAvatarUrl

        // Upload new avatar if changed
        if let data = avatarData {
            do {
                let uploadResult = try await uploader.upload(
                    data: data,
                    mimeType: "image/jpeg",
                    filename: "avatar.jpg",
                    progressHandler: nil
                )
                uploadedAvatarUrl = uploadResult.previewUrl
            } catch {
                self.errorMessage = "Failed to upload avatar: \(error.localizedDescription)"
                self.isSaving = false
                return
            }
        }

        do {
            var payload: [String: String] = [
                "display_name": displayName,
                "bio": bio,
                "website": website,
                "location": location
            ]
            if let avatar = uploadedAvatarUrl {
                payload["avatar_url"] = avatar
            }

            let body = try JSONEncoder().encode(payload)
            let _: UserProfile = try await client.request(
                endpoint: "v1/profiles/me",
                method: "PATCH",
                query: nil,
                body: body
            )
            self.isSuccess = true
        } catch {
            self.errorMessage = error.localizedDescription
        }
        self.isSaving = false
    }
}

public struct EditProfileView: View {
    @State private var viewModel: EditProfileViewModel
    public let onDismiss: () -> Void

    public init(
        currentProfile: UserProfile? = nil,
        client: APIClientProtocol = APIClient(),
        onDismiss: @escaping () -> Void = {}
    ) {
        _viewModel = State(initialValue: EditProfileViewModel(currentProfile: currentProfile, client: client))
        self.onDismiss = onDismiss
    }

    public var body: some View {
        NavigationStack {
            ZStack {
                UsColors.bgPrimary
                    .ignoresSafeArea()

                ScrollView {
                    VStack(spacing: 24) {
                        // Avatar Change Section
                        VStack(spacing: 8) {
                            ZStack {
                                if let data = viewModel.avatarData, let uiImage = UIImage(data: data) {
                                    Image(uiImage: uiImage)
                                        .resizable()
                                        .scaledToFill()
                                        .frame(width: 80, height: 80)
                                        .clipShape(Circle())
                                } else {
                                    UsAvatar(
                                        name: viewModel.displayName.isEmpty ? "User" : viewModel.displayName,
                                        url: viewModel.currentAvatarUrl,
                                        size: .large
                                    )
                                }
                            }

                            PhotosPicker(
                                selection: $viewModel.selectedPhotoItem,
                                matching: .images
                            ) {
                                Text("Change profile photo")
                                    .font(.system(size: 14, weight: .semibold))
                                    .foregroundColor(UsColors.postbookPrimary)
                            }
                            .onChange(of: viewModel.selectedPhotoItem) { _, item in
                                Task { await viewModel.onPhotoSelected(item) }
                            }
                        }
                        .padding(.top, 16)

                        if let err = viewModel.errorMessage {
                            Text(err)
                                .font(.system(size: 13))
                                .foregroundColor(UsColors.statusError)
                        }

                        // Form Fields
                        VStack(spacing: 16) {
                            formField(label: "Name", placeholder: "Your display name", text: $viewModel.displayName)
                            formField(label: "Bio", placeholder: "Bio", text: $viewModel.bio, isMultiline: true)
                            formField(label: "Website", placeholder: "Link", text: $viewModel.website)
                            formField(label: "Location", placeholder: "City, Country", text: $viewModel.location)
                        }
                        .padding(.horizontal, 16)
                    }
                }
            }
            .navigationTitle("Edit Profile")
            .navigationBarTitleDisplayMode(.inline)
            .toolbar {
                ToolbarItem(placement: .cancellationAction) {
                    Button("Cancel", action: onDismiss)
                        .foregroundColor(UsColors.textMuted)
                }

                ToolbarItem(placement: .confirmationAction) {
                    Button("Done") {
                        Task {
                            await viewModel.saveProfile()
                            if viewModel.isSuccess {
                                onDismiss()
                            }
                        }
                    }
                    .font(.system(size: 15, weight: .bold))
                    .foregroundColor(UsColors.postbookPrimary)
                    .disabled(viewModel.isSaving)
                }
            }
        }
    }

    @ViewBuilder
    private func formField(label: String, placeholder: String, text: Binding<String>, isMultiline: Bool = false) -> some View {
        VStack(alignment: .leading, spacing: 6) {
            Text(label)
                .font(.system(size: 13, weight: .medium))
                .foregroundColor(UsColors.textMuted)

            if isMultiline {
                TextEditor(text: text)
                    .scrollContentBackground(.hidden)
                    .background(UsColors.bgSecondary)
                    .foregroundColor(UsColors.textPrimary)
                    .font(.system(size: 15))
                    .padding(8)
                    .clipShape(RoundedRectangle(cornerRadius: 10))
                    .frame(height: 80)
                    .overlay(RoundedRectangle(cornerRadius: 10).stroke(UsColors.borderMedium, lineWidth: 1))
            } else {
                TextField(placeholder, text: text)
                    .textFieldStyle(.plain)
                    .font(.system(size: 15))
                    .foregroundColor(UsColors.textPrimary)
                    .padding(.horizontal, 14)
                    .padding(.vertical, 12)
                    .background(UsColors.bgSecondary)
                    .clipShape(RoundedRectangle(cornerRadius: 10))
                    .overlay(RoundedRectangle(cornerRadius: 10).stroke(UsColors.borderMedium, lineWidth: 1))
            }
        }
    }
}
