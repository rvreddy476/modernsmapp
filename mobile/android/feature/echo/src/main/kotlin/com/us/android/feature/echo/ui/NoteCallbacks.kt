package com.us.android.feature.echo.ui

import com.us.android.feature.echo.data.Note

/** What a note card can do; every action is the host's. */
data class NoteCallbacks(
    val onOpen: (Note) -> Unit,
    val onOpenAuthor: (userId: String) -> Unit,
    val onReply: (Note) -> Unit,
    val onRepost: (Note) -> Unit,
    val onQuote: (Note) -> Unit,
    val onLike: (Note) -> Unit,
    val onShare: (Note) -> Unit,
    /** The ⋮ menu; null draws none. */
    val onMore: ((Note) -> Unit)? = null,
)
