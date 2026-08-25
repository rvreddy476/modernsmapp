import SwiftUI
import UsModel
import UsDesignSystem

public struct CloseFriendContact: Identifiable {
    public let id: String
    public let name: String
    public let username: String
    public var isCloseFriend: Bool

    public init(id: String, name: String, username: String, isCloseFriend: Bool = false) {
        self.id = id
        self.name = name
        self.username = username
        self.isCloseFriend = isCloseFriend
    }
}

public struct CloseFriendsPickerView: View {
    public let onDismiss: () -> Void

    @State private var contacts: [CloseFriendContact] = [
        CloseFriendContact(id: "cf-1", name: "Sarah Chen", username: "sarah_c", isCloseFriend: true),
        CloseFriendContact(id: "cf-2", name: "Marcus Vance", username: "marcus_v", isCloseFriend: true),
        CloseFriendContact(id: "cf-3", name: "Aanya Sharma", username: "aanya_s", isCloseFriend: false),
        CloseFriendContact(id: "cf-4", name: "Dev Patel", username: "dev_p", isCloseFriend: false)
    ]
    @State private var searchQuery: String = ""

    public init(onDismiss: @escaping () -> Void = {}) {
        self.onDismiss = onDismiss
    }

    private var closeFriendsCount: Int {
        contacts.filter { $0.isCloseFriend }.count
    }

    public var body: some View {
        NavigationStack {
            ZStack {
                UsColors.bgPrimary
                    .ignoresSafeArea()

                VStack(spacing: 16) {
                    // Header Banner
                    HStack(spacing: 10) {
                        Image(systemName: "star.circle.fill")
                            .font(.system(size: 28))
                            .foregroundColor(UsColors.onlineGreen)

                        VStack(alignment: .leading, spacing: 2) {
                            Text("Close Friends (\(closeFriendsCount))")
                                .font(.system(size: 15, weight: .bold))
                                .foregroundColor(UsColors.textPrimary)
                            Text("Only people on this list will see your green ring stories.")
                                .font(.system(size: 11))
                                .foregroundColor(UsColors.textMuted)
                        }
                    }
                    .padding(14)
                    .frame(maxWidth: .infinity, alignment: .leading)
                    .background(UsColors.onlineGreen.opacity(0.12))
                    .clipShape(RoundedRectangle(cornerRadius: 14))
                    .padding(.horizontal, 16)

                    // Contacts List
                    ScrollView {
                        LazyVStack(spacing: 8) {
                            ForEach($contacts) { $contact in
                                contactRow(contact: $contact)
                            }
                        }
                        .padding(.horizontal, 16)
                    }

                    Spacer()

                    // Save Button
                    Button(action: {
                        HapticManager.shared.trigger(.success)
                        ToastManager.shared.show("Close Friends list updated!", style: .success)
                        onDismiss()
                    }) {
                        HStack {
                            Spacer()
                            Text("Done (\(closeFriendsCount) Selected)")
                                .font(.system(size: 15, weight: .bold))
                                .foregroundColor(.black)
                            Spacer()
                        }
                        .padding(.vertical, 16)
                        .background(Color.white)
                        .clipShape(RoundedRectangle(cornerRadius: 14))
                    }
                    .padding(16)
                }
                .padding(.top, 8)
            }
            .navigationTitle("Close Friends")
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
    private func contactRow(contact: Binding<CloseFriendContact>) -> some View {
        Button(action: {
            contact.wrappedValue.isCloseFriend.toggle()
            HapticManager.shared.trigger(.selection)
        }) {
            HStack(spacing: 12) {
                UsAvatar(name: contact.wrappedValue.name, size: .small)

                VStack(alignment: .leading, spacing: 2) {
                    Text(contact.wrappedValue.name)
                        .font(.system(size: 14, weight: .semibold))
                        .foregroundColor(UsColors.textPrimary)
                    Text("@\(contact.wrappedValue.username)")
                        .font(.system(size: 12))
                        .foregroundColor(UsColors.textMuted)
                }

                Spacer()

                Image(systemName: contact.wrappedValue.isCloseFriend ? "checkmark.circle.fill" : "circle")
                    .font(.system(size: 22))
                    .foregroundColor(contact.wrappedValue.isCloseFriend ? UsColors.onlineGreen : UsColors.textDim)
            }
            .padding(12)
            .background(UsColors.bgSecondary)
            .clipShape(RoundedRectangle(cornerRadius: 12))
        }
        .buttonStyle(.plain)
    }
}
