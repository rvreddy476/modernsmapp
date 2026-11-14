package com.us.android.feature.echo.ui

import androidx.compose.runtime.Immutable
import com.us.android.feature.echo.data.Note

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

/** Whether [next] continues [note] as the same author's next thread part (the thin accent line). */
fun continuesThread(note: Note, next: Note?): Boolean =
    next != null && next.replyToId == note.id && next.authorId == note.authorId

const val MAX_BUBBLES = 2
const val MAX_REPLY_DEPTH = 2
private const val MIN_THREAD_PARTS = 2
private const val LONG_NOTE_CHARS = 280
private const val EDITED = "Edited"
private const val SEPARATOR = " · "
