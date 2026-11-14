package com.us.android.feature.echo.ui

import androidx.compose.runtime.Composable
import androidx.compose.runtime.remember
import com.us.android.feature.echo.data.Note

/** What a note card can do; every action is the host's. */
data class NoteCallbacks(
    val onOpen: (Note) -> Unit,
    val onOpenAuthor: (userId: String) -> Unit,
    val onReply: (Note) -> Unit,
    /** The repost button: X opens a sheet (Repost / Undo repost, Quote). */
    val onRepost: (Note) -> Unit,
    val onQuote: (Note) -> Unit,
    val onLike: (Note) -> Unit,
    val onShare: (Note) -> Unit,
    /** The ··· menu; null draws none. */
    val onMore: ((Note) -> Unit)? = null,
    /** The bookmark; null draws none. */
    val onBookmark: ((Note) -> Unit)? = null,
    /** A tap on a poll choice. */
    val onVote: ((Note, Int) -> Unit)? = null,
    /** Who is looking: a poll's author sees results, never the vote buttons. */
    val viewerId: String = "",
)

/**
 * The callbacks every Echo list wires the same way: cards open their note,
 * authors open Echo's profile, and every action goes to [actions]. Replies
 * and quotes go through the host's gates ([onReply], [onQuote]).
 */
@Composable
fun rememberNoteCallbacks(
    actions: EchoNoteActions,
    destinations: EchoDestinations,
    onReply: (Note) -> Unit,
    onQuote: (Note) -> Unit,
    onOpen: (Note) -> Unit = { destinations.onOpenNote(it.id) },
): NoteCallbacks = remember(actions, destinations, onReply, onQuote, onOpen) {
    NoteCallbacks(
        onOpen = onOpen,
        onOpenAuthor = destinations.onOpenEchoProfile,
        onReply = onReply,
        onRepost = actions::openRepost,
        onQuote = onQuote,
        onLike = actions::toggleLike,
        onShare = actions::openShare,
        onMore = actions::openMenu,
        onBookmark = actions::toggleBookmark,
        onVote = actions::vote,
        viewerId = actions.viewerId,
    )
}
