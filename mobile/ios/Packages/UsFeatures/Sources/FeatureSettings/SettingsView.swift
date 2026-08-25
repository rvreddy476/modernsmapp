import SwiftUI
import UsModel
import UsDesignSystem
import UsNetwork

public struct SettingsView: View {
    // Privacy state
    @State private var isPrivateAccount: Bool = false
    @State private var allowMentionsFromEveryone: Bool = true
    @State private var showOnlineStatus: Bool = true
    @State private var sendReadReceipts: Bool = true
    @State private var allowStoryResharing: Bool = true
    @State private var manualTagApproval: Bool = false
    @State private var messagePermission: String = "Everyone"

    // Security state
    @State private var isBiometricsEnabled: Bool = true
    @State private var isTwoFactorEnabled: Bool = true

    // Data & Storage
    @State private var cacheCleared: Bool = false
    @State private var highQualityCellular: Bool = true

    // Dialogs & Modals
    @State private var showSignOutAlert: Bool = false
    @State private var showLanguagePicker: Bool = false
    @State private var selectedLanguage: String = "English"

    private let sessionManager: SessionManager

    public init(sessionManager: SessionManager = .shared) {
        self.sessionManager = sessionManager
    }

    public var body: some View {
        NavigationStack {
            ZStack {
                UsColors.bgPrimary
                    .ignoresSafeArea()

                List {
                    // Account Profile Header
                    Section {
                        if let session = sessionManager.currentSession {
                            HStack(spacing: 14) {
                                UsAvatar(name: session.displayName ?? session.username ?? "User", size: .large)
                                VStack(alignment: .leading, spacing: 2) {
                                    HStack(spacing: 4) {
                                        Text(session.displayName ?? session.username ?? "User")
                                            .font(.system(size: 16, weight: .bold))
                                            .foregroundColor(UsColors.textPrimary)
                                        Image(systemName: "checkmark.seal.fill")
                                            .foregroundColor(UsColors.postbookPrimary)
                                            .font(.system(size: 12))
                                    }

                                    if let user = session.username {
                                        Text("@\(user)")
                                            .font(.system(size: 13))
                                            .foregroundColor(UsColors.textMuted)
                                    }
                                }

                                Spacer()

                                NavigationLink(destination: accountDetailsSubView) {
                                    EmptyView()
                                }
                                .opacity(0)
                            }
                            .padding(.vertical, 4)
                        }
                    } header: {
                        Text("Account")
                            .foregroundColor(UsColors.textMuted)
                    }
                    .listRowBackground(UsColors.bgSecondary)

                    // Granular Privacy Controls
                    Section {
                        Toggle(isOn: $isPrivateAccount) {
                            VStack(alignment: .leading, spacing: 2) {
                                Text("Private Account")
                                    .foregroundColor(UsColors.textPrimary)
                                Text("Only approved followers can see your posts & reels")
                                    .font(.system(size: 11))
                                    .foregroundColor(UsColors.textMuted)
                            }
                        }
                        .tint(UsColors.postbookPrimary)

                        Toggle("Show Active / Online Status", isOn: $showOnlineStatus)
                            .tint(UsColors.onlineGreen)
                            .foregroundColor(UsColors.textPrimary)

                        Toggle("Send Read Receipts in DMs", isOn: $sendReadReceipts)
                            .tint(UsColors.postbookPrimary)
                            .foregroundColor(UsColors.textPrimary)

                        Toggle("Allow Story Resharing", isOn: $allowStoryResharing)
                            .tint(UsColors.postbookPrimary)
                            .foregroundColor(UsColors.textPrimary)

                        Toggle("Manual Tag & Mention Approval", isOn: $manualTagApproval)
                            .tint(UsColors.postbookPrimary)
                            .foregroundColor(UsColors.textPrimary)

                        NavigationLink(destination: messagePrivacySubView) {
                            HStack {
                                Text("Who Can Message You")
                                    .foregroundColor(UsColors.textPrimary)
                                Spacer()
                                Text(messagePermission)
                                    .font(.system(size: 13))
                                    .foregroundColor(UsColors.textMuted)
                            }
                        }

                        NavigationLink(destination: blockedAccountsSubView) {
                            Text("Blocked & Muted Accounts")
                                .foregroundColor(UsColors.textPrimary)
                        }
                    } header: {
                        Text("Privacy Controls")
                            .foregroundColor(UsColors.textMuted)
                    }
                    .listRowBackground(UsColors.bgSecondary)

                    // Security & Biometrics
                    Section {
                        Toggle("Face ID / Touch ID App Lock", isOn: $isBiometricsEnabled)
                            .tint(UsColors.postbookPrimary)
                            .foregroundColor(UsColors.textPrimary)

                        Toggle("Two-Factor Authentication (2FA)", isOn: $isTwoFactorEnabled)
                            .tint(UsColors.onlineGreen)
                            .foregroundColor(UsColors.textPrimary)

                        NavigationLink(destination: loginDevicesSubView) {
                            HStack {
                                Text("Active Login Sessions")
                                    .foregroundColor(UsColors.textPrimary)
                                Spacer()
                                Text("2 Devices")
                                    .font(.system(size: 12))
                                    .foregroundColor(UsColors.onlineGreen)
                            }
                        }
                    } header: {
                        Text("Security")
                            .foregroundColor(UsColors.textMuted)
                    }
                    .listRowBackground(UsColors.bgSecondary)

                    // App Preferences & Localization
                    Section {
                        Button(action: { showLanguagePicker = true }) {
                            HStack {
                                Text("App Language")
                                    .foregroundColor(UsColors.textPrimary)
                                Spacer()
                                Text(selectedLanguage)
                                    .font(.system(size: 13))
                                    .foregroundColor(UsColors.postbookPrimary)
                            }
                        }

                        Toggle("Upload Highest Quality on Cellular", isOn: $highQualityCellular)
                            .tint(UsColors.postbookPrimary)
                            .foregroundColor(UsColors.textPrimary)
                    } header: {
                        Text("Preferences")
                            .foregroundColor(UsColors.textMuted)
                    }
                    .listRowBackground(UsColors.bgSecondary)

                    // Storage, Media & Cache
                    Section {
                        Button(action: {
                            FeedCacheStore.shared.clear()
                            cacheCleared = true
                            HapticManager.shared.trigger(.success)
                            ToastManager.shared.show("Cache cleared! 142 MB freed", style: .success)
                        }) {
                            HStack {
                                Text("Clear Media & Feed Cache")
                                    .foregroundColor(UsColors.textPrimary)
                                Spacer()
                                if cacheCleared {
                                    Text("Cleared (0 MB)")
                                        .font(.system(size: 12, weight: .semibold))
                                        .foregroundColor(UsColors.onlineGreen)
                                } else {
                                    Text("142 MB")
                                        .font(.system(size: 12))
                                        .foregroundColor(UsColors.textMuted)
                                }
                            }
                        }

                        Button(action: {
                            HapticManager.shared.trigger(.success)
                            ToastManager.shared.show("Data export archive requested via email!", style: .info)
                        }) {
                            Text("Download My Data & Post Archive")
                                .foregroundColor(UsColors.postbookPrimary)
                        }
                    } header: {
                        Text("Storage & Data")
                            .foregroundColor(UsColors.textMuted)
                    }
                    .listRowBackground(UsColors.bgSecondary)

                    // Session Sign Out
                    Section {
                        Button(role: .destructive, action: { showSignOutAlert = true }) {
                            HStack {
                                Spacer()
                                Text("Sign Out")
                                    .font(.system(size: 15, weight: .bold))
                                    .foregroundColor(UsColors.statusError)
                                Spacer()
                            }
                        }
                    }
                    .listRowBackground(UsColors.bgSecondary)
                }
                .scrollContentBackground(.hidden)
            }
            .navigationTitle("Settings & Privacy")
            .navigationBarTitleDisplayMode(.inline)
            .confirmationDialog("Select Language", isPresented: $showLanguagePicker) {
                Button("English") { selectedLanguage = "English" }
                Button("हिंदी (Hindi)") { selectedLanguage = "हिंदी" }
                Button("తెలుగు (Telugu)") { selectedLanguage = "తెలుగు" }
                Button("தமிழ் (Tamil)") { selectedLanguage = "தமிழ்" }
                Button("ಕನ್ನಡ (Kannada)") { selectedLanguage = "ಕನ್ನಡ" }
                Button("Cancel", role: .cancel) {}
            }
            .alert("Sign Out", isPresented: $showSignOutAlert) {
                Button("Cancel", role: .cancel) {}
                Button("Sign Out", role: .destructive) {
                    sessionManager.clearSession()
                }
            } message: {
                Text("Are you sure you want to sign out of your account on this device?")
            }
        }
    }

    // Subviews
    private var accountDetailsSubView: some View {
        ZStack {
            UsColors.bgPrimary.ignoresSafeArea()
            VStack(alignment: .leading, spacing: 16) {
                Text("Account Information")
                    .font(.system(size: 16, weight: .bold))
                    .foregroundColor(UsColors.textPrimary)

                VStack(spacing: 12) {
                    infoRow(label: "Phone Number", value: "+91 98765 43210")
                    infoRow(label: "Email", value: "user@example.com")
                    infoRow(label: "Joined", value: "August 2024")
                    infoRow(label: "Account Type", value: "Creator Account 🌟")
                }
                .padding(14)
                .background(UsColors.bgSecondary)
                .clipShape(RoundedRectangle(cornerRadius: 14))

                Spacer()
            }
            .padding(16)
        }
        .navigationTitle("Account Details")
    }

    private var messagePrivacySubView: some View {
        ZStack {
            UsColors.bgPrimary.ignoresSafeArea()
            List {
                Section {
                    Button(action: { messagePermission = "Everyone" }) {
                        HStack {
                            Text("Everyone")
                                .foregroundColor(UsColors.textPrimary)
                            Spacer()
                            if messagePermission == "Everyone" {
                                Image(systemName: "checkmark")
                                    .foregroundColor(UsColors.postbookPrimary)
                            }
                        }
                    }
                    Button(action: { messagePermission = "People You Follow" }) {
                        HStack {
                            Text("People You Follow")
                                .foregroundColor(UsColors.textPrimary)
                            Spacer()
                            if messagePermission == "People You Follow" {
                                Image(systemName: "checkmark")
                                    .foregroundColor(UsColors.postbookPrimary)
                            }
                        }
                    }
                    Button(action: { messagePermission = "No One" }) {
                        HStack {
                            Text("No One")
                                .foregroundColor(UsColors.textPrimary)
                            Spacer()
                            if messagePermission == "No One" {
                                Image(systemName: "checkmark")
                                    .foregroundColor(UsColors.postbookPrimary)
                            }
                        }
                    }
                }
                .listRowBackground(UsColors.bgSecondary)
            }
            .scrollContentBackground(.hidden)
        }
        .navigationTitle("Direct Messages")
    }

    private var blockedAccountsSubView: some View {
        ZStack {
            UsColors.bgPrimary.ignoresSafeArea()
            UsEmptyState(title: "No Blocked Accounts", detail: "You have not blocked or restricted any accounts.")
        }
        .navigationTitle("Blocked Accounts")
    }

    private var loginDevicesSubView: some View {
        ZStack {
            UsColors.bgPrimary.ignoresSafeArea()
            List {
                Section {
                    HStack(spacing: 12) {
                        Image(systemName: "iphone")
                            .font(.system(size: 22))
                            .foregroundColor(UsColors.onlineGreen)
                        VStack(alignment: .leading, spacing: 2) {
                            Text("iPhone 15 Pro Max (This Device)")
                                .font(.system(size: 14, weight: .bold))
                                .foregroundColor(UsColors.textPrimary)
                            Text("Bangalore, India • Active now")
                                .font(.system(size: 11))
                                .foregroundColor(UsColors.textMuted)
                        }
                    }

                    HStack(spacing: 12) {
                        Image(systemName: "laptopcomputer")
                            .font(.system(size: 22))
                            .foregroundColor(UsColors.textMuted)
                        VStack(alignment: .leading, spacing: 2) {
                            Text("MacBook Pro 16\" (Chrome)")
                                .font(.system(size: 14, weight: .bold))
                                .foregroundColor(UsColors.textPrimary)
                            Text("Bangalore, India • 2 hours ago")
                                .font(.system(size: 11))
                                .foregroundColor(UsColors.textMuted)
                        }
                    }
                } header: {
                    Text("Where You're Logged In")
                        .foregroundColor(UsColors.textMuted)
                }
                .listRowBackground(UsColors.bgSecondary)
            }
            .scrollContentBackground(.hidden)
        }
        .navigationTitle("Login Sessions")
    }

    @ViewBuilder
    private func infoRow(label: String, value: String) -> some View {
        HStack {
            Text(label)
                .font(.system(size: 13))
                .foregroundColor(UsColors.textMuted)
            Spacer()
            Text(value)
                .font(.system(size: 13, weight: .semibold))
                .foregroundColor(UsColors.textPrimary)
        }
    }
}
