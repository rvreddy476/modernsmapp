package com.us.android.core.engagement.data

/**
 * What a comment row's ⋮ can offer (2026-10-14, audit T-17), with the label
 * the viewer reads. Momentum's words; the order on screen is alphabetical by
 * label, the rule for every menu (see [commentMenuActions]).
 */
enum class CommentMenuAction(val label: String, val destructive: Boolean = false) {
    DELETE("Delete", destructive = true),
    HEART("Heart"),
    UNHEART("Remove heart"),
    PIN("Pin"),
    UNPIN("Unpin"),
    REPORT("Report", destructive = true),
}

/**
 * The permission matrix for one row's ⋮, mirroring what post-service will
 * actually do so the menu never offers a request that is bound to fail:
 *
 *  - **Report** — anyone else's comment, never the viewer's own (trust-safety
 *    takes `comment` reports from any signed-in viewer).
 *  - **Delete** — the viewer's OWN comment only. `SoftDeleteComment` refuses
 *    every other caller with `NOT_COMMENT_AUTHOR`, the post's creator
 *    included; there is no creator delete on the server, so none here.
 *  - **Heart / Remove heart** — the post's creator only (`ErrNotPostAuthor`
 *    otherwise), on a comment or a reply.
 *  - **Pin / Unpin** — the post's creator only, top-level comments only
 *    (`CANNOT_PIN_REPLY`).
 *
 * A row still being written ([CommentRow.pending]) has no id the server
 * knows, so it has no menu. A blank [viewerId] (profile not loaded yet)
 * cannot be the author or the creator, so it is offered Report alone.
 */
fun commentMenuActions(row: CommentRow, viewerId: String?, postAuthorId: String?): List<CommentMenuAction> {
    if (row.pending) return emptyList()
    val viewer = viewerId.orEmpty()
    val isOwn = viewer.isNotBlank() && row.authorId == viewer
    val isCreator = viewer.isNotBlank() && postAuthorId == viewer
    return buildList {
        if (!isOwn) add(CommentMenuAction.REPORT)
        if (isOwn) add(CommentMenuAction.DELETE)
        if (isCreator) {
            add(if (row.heartedByAuthor) CommentMenuAction.UNHEART else CommentMenuAction.HEART)
            if (!row.isReply) add(if (row.pinned) CommentMenuAction.UNPIN else CommentMenuAction.PIN)
        }
    }.sortedWith(compareBy(String.CASE_INSENSITIVE_ORDER) { it.label })
}

// ── @mentions ───────────────────────────────────────────────────────────

/** An `@handle` being typed at the END of the draft: `@` after a start or a space. */
private val TRAILING_MENTION = Regex("""(?:^|\s)@([A-Za-z0-9_.]{1,30})$""")

/**
 * The handle being typed at the end of [draft], or null when the draft does
 * not end in one. The composer is a plain string field, so "the caret" is
 * taken to be the end — where it is while someone is typing.
 */
fun activeMentionQuery(draft: String): String? = TRAILING_MENTION.find(draft)?.groupValues?.get(1)

/** Replaces the trailing `@partial` with `@username ` (a space, so typing simply continues). */
fun completeMention(draft: String, username: String): String {
    val match = TRAILING_MENTION.find(draft) ?: return draft
    val at = draft.lastIndexOf('@', startIndex = draft.length - 1).takeIf { it >= match.range.first } ?: return draft
    return draft.substring(0, at) + "@" + username + " "
}
