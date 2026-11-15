package com.us.android.feature.echo.ui

import androidx.compose.runtime.Immutable
import com.us.android.feature.echo.data.Note
import com.us.android.feature.echo.data.compactCount

/*
 * The words and the shape of Echo's "serif conversation cards"
 * (docs/designs/echo-look-v3.md), as plain functions so the rules are tested
 * without a screen: the topic line on top of a note, the reply bubbles under
 * it on a timeline, the indent of a reply on a note's page, and the line that
 * joins a thread's parts.
 */

/**
 * The small line on top of every note. [accent] is true when it names
 * something (a hashtag, a thread, a quote, a reply) and is drawn in Echo's
 * ember; time alone is secondary text.
 */
@Immutable
data class TopicLine(val text: String, val accent: Boolean)

/**
 * `#firsthashtag · 2h`, or `Thread · 3 parts · 5h`, `Quote · 2h`,
 * `Reply to @name · 2h`; nothing to name means just the time. A thread wins
 * over a reply (its later parts are replies to its earlier ones), a reply
 * over a quote, a quote over a hashtag. [threadParts] is the count when it is
 * known; a thread whose length is not known reads `Thread · 5h`.
 * [replyToHandle] is the parent's @handle when the screen has it.
 */
fun topicLine(
    note: Note,
    age: String,
    thread: Boolean = false,
    threadParts: Int? = null,
    replyToHandle: String? = null,
): TopicLine {
    val subject = when {
        thread -> threadParts?.takeIf { it >= MIN_THREAD_PARTS }?.let { "Thread · $it parts" } ?: "Thread"
        note.replyToId != null -> replyToHandle?.takeIf { it.isNotBlank() }?.let { "Reply to $it" } ?: "Reply"
        note.quoteOfId != null -> "Quote"
        else -> firstHashtag(note.text)?.let { "#$it" }
    }
    val parts = listOfNotNull(subject, age.takeIf { it.isNotBlank() }, EDITED.takeIf { note.edited })
    return TopicLine(text = parts.joinToString(SEPARATOR), accent = subject != null)
}

/** The note's first hashtag, without its `#`, by the same rules its links use; null when it has none. */
fun firstHashtag(text: String): String? =
    echoTokens(text).firstNotNullOfOrNull { (it as? EchoToken.Hashtag)?.tag?.removePrefix("#") }

/** A timeline note is "long" past this many characters, and is set a size smaller. */
fun isLongNote(text: String): Boolean = text.length > LONG_NOTE_CHARS

/**
 * What a timeline card shows of its conversation: up to two top replies as
 * bubbles, "N replies" (which opens the note), and [thread] when one of the
 * first replies is the author continuing their own note (a thread), which
 * reads "Read the thread" instead of a bubble of their own.
 */
@Immutable
data class ReplyPreview(val bubbles: List<Note>, val label: String?, val thread: Boolean)

/**
 * The card's preview from the first [replies] the server returned for
 * [note]; null (no bubble area at all) while they have not arrived, or when
 * there are none.
 */
fun replyPreview(note: Note, replies: List<Note>?): ReplyPreview? {
    if (replies == null) return null
    val (own, others) = replies.partition { it.authorId == note.authorId && it.replyToId == note.id }
    val bubbles = others.take(MAX_BUBBLES)
    val count = maxOf(note.replyCount, replies.size.toLong())
    if (bubbles.isEmpty() && own.isEmpty()) return null
    return ReplyPreview(bubbles = bubbles, label = repliesLabel(count), thread = own.isNotEmpty())
}

/** "1 reply", "12 replies"; null for none. */
fun repliesLabel(count: Long): String? = when {
    count <= 0 -> null
    count == 1L -> "1 reply"
    else -> "$count replies"
}

/** "3 echoes", "1 echo", "42 hearts": a count pill's words. */
fun countWords(count: Long, one: String, many: String): String =
    if (count == 1L) "1 $one" else "$count $many"

/** "0 hearts", "1 heart", "1.2K hearts": [countWords] with the count shortened, for the note page's grid. */
fun compactCountWords(count: Long, one: String, many: String): String = when {
    count == 1L -> "1 $one"
    count <= 0L -> "0 $many"
    else -> "${compactCount(count)} $many"
}

/** "1 view", "1.2K views": the views pill's words (and the card's Eye description). */
fun viewsWords(count: Long): String = compactCountWords(count, "view", "views")

/** "Echo it", or "Echo it 3" once the note has been re-echoed. */
fun echoItLabel(count: Long): String = compactCount(count).let { if (it.isEmpty()) ECHO_IT else "$ECHO_IT $it" }

/**
 * A timeline card's action row, left to right, on one line spread evenly
 * across the card: Reply, Echo it (count), the heart (count), the Eye
 * (views; opens the note) and More.
 */
enum class CardAction { REPLY, ECHO, HEART, VIEWS, MORE }

/** The card's row in order. */
val CARD_ACTIONS: List<CardAction> = CardAction.entries.toList()

/** One cell of the note page's pill grid. */
enum class FocusPill { HEARTS, ECHOES, VIEWS, SAVE, SHARE, MORE }

/**
 * The note page's pills as an even grid, 3 equal columns by 2 rows:
 * hearts, echoes, views; then Save, Share, More. Quotes are not a pill:
 * "View quotes" sits on the Conversation line.
 */
val FOCUS_PILL_ROWS: List<List<FocusPill>> = listOf(
    listOf(FocusPill.HEARTS, FocusPill.ECHOES, FocusPill.VIEWS),
    listOf(FocusPill.SAVE, FocusPill.SHARE, FocusPill.MORE),
)

/** The Conversation line's link to a note's quotes. */
const val VIEW_QUOTES = "View quotes"

/**
 * One reply on a note's page: [depth] steps of indent (0 to [MAX_REPLY_DEPTH]),
 * and [replyingTo] (an @handle) when it answers a reply deeper than the indent
 * goes, so it is drawn flat with "Replying to @x".
 */
@Immutable
data class ReplyRow(val note: Note, val depth: Int, val replyingTo: String?)

/**
 * The page's replies with their indent: a reply to the note itself is 0, a
 * reply to a reply shown above it one step more, up to [MAX_REPLY_DEPTH];
 * deeper than that stays at the last step and says who it answers. A reply
 * whose parent is not on the list is drawn at 0.
 */
fun replyRows(focusId: String, replies: List<Note>): List<ReplyRow> {
    val depths = HashMap<String, Int>()
    val byId = replies.associateBy { it.id }
    return replies.map { reply ->
        val parent = reply.replyToId
        val parentDepth = parent?.takeIf { it != focusId }?.let(depths::get)
        val wanted = if (parentDepth == null) 0 else parentDepth + 1
        val depth = wanted.coerceAtMost(MAX_REPLY_DEPTH)
        depths[reply.id] = wanted
        val replyingTo = if (wanted > MAX_REPLY_DEPTH) {
            parent?.let(byId::get)?.author?.handle?.takeIf { it.isNotBlank() }
        } else {
            null
        }
        ReplyRow(note = reply, depth = depth, replyingTo = replyingTo)
    }
}

/**
 * How many parts of a thread end at [focus]: the focused note and each
 * parent above it (in [conversation], top first) by the same author, each
 * replying to the one before. 1 when the note is not a thread part.
 */
fun threadPartsEndingAt(conversation: List<Note>, focus: Note): Int {
    var parts = 1
    var child = focus
    for (parent in conversation.asReversed()) {
        if (child.replyToId != parent.id || parent.authorId != focus.authorId) break
        parts++
        child = parent
    }
    return parts
}

/** The thread parts after [noteId], in order; empty when the note is not in [thread]. */
fun threadPartsAfter(noteId: String, thread: List<Note>): List<Note> {
    val i = thread.indexOfFirst { it.id == noteId }
    return if (i < 0) emptyList() else thread.drop(i + 1)
}

/** Whether [next] continues [note] as the same author's next thread part (the thin accent line). */
fun continuesThread(note: Note, next: Note?): Boolean =
    next != null && next.replyToId == note.id && next.authorId == note.authorId

const val MAX_BUBBLES = 2
const val MAX_REPLY_DEPTH = 2
internal const val MIN_THREAD_PARTS = 2
private const val LONG_NOTE_CHARS = 280
private const val EDITED = "Edited"
private const val SEPARATOR = " · "

/**
 * Whether a timeline card is a thread: the server's part count on its first
 * note (`thread_parts`), or the author's own reply among its first replies.
 */
fun isThreadCard(note: Note, preview: ReplyPreview?): Boolean =
    note.threadParts >= MIN_THREAD_PARTS || preview?.thread == true

/**
 * A reply's own replies, opened under it on the note page with "View N
 * replies": what has arrived, whether a page is on its way or failed, and
 * the cursor of the next page (null when there is none).
 */
@Immutable
data class ChildReplies(
    val items: List<Note> = emptyList(),
    val loading: Boolean = true,
    val error: Boolean = false,
    val cursor: String? = null,
)

/** One line of the note page's replies. */
sealed interface ReplyItem {
    val key: String

    /** A reply bubble. */
    data class Reply(val row: ReplyRow) : ReplyItem {
        override val key: String get() = "r:" + row.note.id
    }

    /** "View N replies" under [parent], whose own replies are not open yet. */
    data class ViewReplies(val parent: Note, val depth: Int) : ReplyItem {
        override val key: String get() = "v:" + parent.id
    }

    /** Under [parentId]'s opened replies: loading, a retry, or "Show more replies". */
    data class ChildStatus(val parentId: String, val depth: Int, val state: ChildReplies) : ReplyItem {
        override val key: String get() = "s:$parentId"
    }
}

/**
 * Where a reply opened under a reply sits: one step further in, up to
 * [MAX_REPLY_DEPTH]; past that it stays at the last step and says whose
 * reply it answers ([parentHandle], "Replying to @x").
 */
fun childReplyPlacement(parentDepth: Int, parentHandle: String?): Pair<Int, String?> {
    val level = parentDepth + 1
    return if (level <= MAX_REPLY_DEPTH) level to null else MAX_REPLY_DEPTH to parentHandle?.takeIf { it.isNotBlank() }
}

/** "View 1 reply", "View 12 replies". */
fun viewRepliesLabel(count: Long): String = "View " + countWords(count, "reply", "replies")

/**
 * The note page's replies with each reply's own replies where they were
 * opened ([open], by parent id): the direct replies as [replyRows] lays
 * them out, a "View N replies" line under a reply that has replies and is
 * not open, and under an open one its replies (one step in) and their status.
 * A note shows once, wherever it first appears.
 */
fun replyItems(focusId: String, replies: List<Note>, open: Map<String, ChildReplies>): List<ReplyItem> {
    val out = ArrayList<ReplyItem>()
    val seen = HashSet<String>()
    fun emit(row: ReplyRow) {
        if (!seen.add(row.note.id)) return
        out += ReplyItem.Reply(row)
        val kids = open[row.note.id]
        if (kids == null) {
            if (row.note.replyCount > 0) out += ReplyItem.ViewReplies(row.note, row.depth)
            return
        }
        val (depth, replyingTo) = childReplyPlacement(row.depth, row.note.author?.handle)
        kids.items.forEach { emit(ReplyRow(note = it, depth = depth, replyingTo = replyingTo)) }
        if (kids.loading || kids.error || kids.cursor != null) {
            out += ReplyItem.ChildStatus(row.note.id, depth, kids)
        }
    }
    replyRows(focusId, replies).forEach(::emit)
    return out
}
