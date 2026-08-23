package com.us.android.feature.notifications.permission

/**
 * What the app should do about the notification permission, right now.
 *
 * A closed decision rather than a boolean, because "we cannot post
 * notifications" has three causes that need three different responses, and
 * conflating them is how apps end up either nagging or silently broken.
 */
enum class NotificationPermissionAction {
    /** Nothing to do: granted, or a platform that has no such permission. */
    None,

    /** Ask the system. The user has not been asked yet. */
    Request,

    /**
     * The system will no longer show a prompt. Offer a route to app settings.
     *
     * Calling `requestPermission` in this state does nothing at all — the
     * callback fires immediately with "denied" and no UI appears. An app that
     * keeps calling it looks broken to the user and to the developer reading
     * the logs.
     */
    DirectToSettings,
}

/**
 * Decides what to do about POST_NOTIFICATIONS — Slice D, D-D2.
 *
 * ## WHY THIS IS A PURE FUNCTION
 *
 * The rule reads simply and is wrong in three subtle ways if written inline
 * against the platform APIs, so it is separated and tabled.
 *
 * ## THE `shouldShowRationale` TRAP
 *
 * `shouldShowRequestPermissionRationale` returns **false in two opposite
 * situations**: before the user has ever been asked, and after they have
 * permanently denied. It only returns true in the middle state — asked once,
 * declined once, still askable.
 *
 * So the platform alone cannot distinguish "never asked" from "asked and shut
 * the door". [hasAskedBefore] — which the app persists itself — is what
 * separates them. Without it an app either never asks, or asks forever into a
 * dialog that no longer appears.
 *
 * ## WHY BELOW API 33 IS [NotificationPermissionAction.None]
 *
 * POST_NOTIFICATIONS did not exist before Android 13; notifications are
 * permitted by default. Requesting it there is not merely useless, it resolves
 * as denied and would make a perfectly working install look broken.
 */
object NotificationPermissionPolicy {

    /** Android 13. Below this the permission does not exist. */
    const val FIRST_SDK_REQUIRING_PERMISSION = 33

    fun decide(
        sdkInt: Int,
        isGranted: Boolean,
        hasAskedBefore: Boolean,
        shouldShowRationale: Boolean,
    ): NotificationPermissionAction = when {
        // Pre-Android 13: permitted by default, nothing to request.
        sdkInt < FIRST_SDK_REQUIRING_PERMISSION -> NotificationPermissionAction.None

        isGranted -> NotificationPermissionAction.None

        // Never asked. The system will show a real dialog.
        !hasAskedBefore -> NotificationPermissionAction.Request

        // Asked, declined once, still askable — the system will show the
        // dialog again, and the user has seen enough of the app to judge.
        shouldShowRationale -> NotificationPermissionAction.Request

        // Asked and permanently denied. Only Settings can change this now.
        else -> NotificationPermissionAction.DirectToSettings
    }
}
