package com.us.android.core.ui

/**
 * What the post "more" sheet can ask its host to do.
 *
 * A bundle rather than ten parameters, remembered by the host so its
 * identity is stable. Every action is fire-and-forget from the sheet's side:
 * the host owns the optimistic state and the network, the sheet only closes
 * itself where the design says the action is complete on the tap.
 */
// One parameter per row action: the bundle IS the parameter list.
@Suppress("LongParameterList")
class UsPostMoreCallbacks(
    val onToggleSave: () -> Unit,
    /** The system share sheet; the host also records the external share. */
    val onShare: () -> Unit,
    val onInterested: () -> Unit,
    /** The host removes the post from the list at once and tells the server. */
    val onNotInterested: () -> Unit,
    val onFollow: () -> Unit,
    val onUnfollow: () -> Unit,
    /** Confirmed in the sheet; the host removes every post by the author. */
    val onBlock: () -> Unit,
    /** The chosen reason and, for "Other", the words typed under it. */
    val onReport: (reason: UsReportReason, details: String) -> Unit,
    /**
     * Confirmed in the sheet, own posts only. The host sends the soft delete
     * and reports back through `UsPostMoreState.delete`; the sheet waits for
     * that rather than closing on the tap, so a refusal is read where the
     * viewer is still looking.
     */
    val onDelete: () -> Unit = {},
    /**
     * Reels only: "Clear screen" / "Show controls" — the host flips its
     * mode; the sheet has already left when this fires, so the frame that
     * clears is the one the viewer is looking at.
     */
    val onClearScreen: () -> Unit = {},
    /** Reels only: a rung of the quality picker was picked; the host applies and keeps it. */
    val onSelectQuality: (UsReelQuality) -> Unit = {},
)
