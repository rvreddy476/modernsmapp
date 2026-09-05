package com.us.android.feature.chat.ui.community

/** Where a community page's header and ≡ can send the user. */
data class CommunityPageDestinations(
    val onBack: () -> Unit,
    val onEdit: (communityId: String) -> Unit,
    val onAdmins: (communityId: String) -> Unit,
    val onPost: (communityId: String) -> Unit,
    /** The page closed itself — the viewer left or deleted the community. */
    val onClosed: () -> Unit,
)
