package com.us.android.feature.notifications.ui

import com.us.android.core.model.NotificationKind

/**
 * The buttons a row of the inbox carries under or beside its sentence. Pure,
 * so the table is a test.
 *
 * Connect is gone (founder, 24 Oct 2026; graph-service answers 410
 * CONNECTIONS_RETIRED to an accept): an old connection-request row offers
 * nothing to press. It says [CONNECTIONS_ENDED] instead, and a tap still opens
 * the person's profile, where Follow and Message are.
 */
internal enum class RowInlineActions {
    /** Accept / Decline / Block, while the message request still waits. */
    MessageRequest,

    /** Accept / Decline on a private account's incoming follow request. */
    FollowRequest,

    /** Follow back, or "Following". */
    FollowBack,
}

internal fun inlineActionsFor(kind: NotificationKind): RowInlineActions? = when (kind) {
    NotificationKind.MessageRequest -> RowInlineActions.MessageRequest
    NotificationKind.FollowRequest -> RowInlineActions.FollowRequest
    NotificationKind.Follow -> RowInlineActions.FollowBack
    else -> null
}

/** What an old connection-request row says, the web's words (notificationInbox.ts). */
internal const val CONNECTIONS_ENDED = "Connections have ended. Follow or message them instead."
