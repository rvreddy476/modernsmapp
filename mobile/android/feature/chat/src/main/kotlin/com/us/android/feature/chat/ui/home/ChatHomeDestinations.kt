package com.us.android.feature.chat.ui.home

/** Where the one chat screen's header, tabs and cards can send the user. */
data class ChatHomeDestinations(
    /** Null when the screen is the Messages root — a root has no back arrow. */
    val onBack: (() -> Unit)?,
    val onOpenThread: (conversationId: String, title: String, isGroup: Boolean) -> Unit,
    val onOpenRequests: () -> Unit,
    val onOpenInvitations: () -> Unit,
    val onCreateGroup: () -> Unit,
    val onCreateCommunity: () -> Unit,
    val onOpenCommunity: (communityId: String) -> Unit,
    val onJoinWithLink: () -> Unit,
    val onOpenLockSettings: () -> Unit,
    val onOpenCallHistory: () -> Unit,
    /** Chat parity: the header's add-people glyph — the new-message people picker. */
    val onNewMessage: () -> Unit = onCreateGroup,
    /** Chat parity: the ≡ menu's "Archived chats". */
    val onOpenArchived: () -> Unit = {},
    /** Hidden chats: the list a secret key in search revealed. */
    val onOpenHiddenChats: () -> Unit = {},
    /** Hidden chats: "Hide chat" before setup — the setup page, then this chat is hidden. */
    val onSetUpHiddenChats: (conversationId: String) -> Unit = {},
)
