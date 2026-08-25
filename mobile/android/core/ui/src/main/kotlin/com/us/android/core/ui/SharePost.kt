package com.us.android.core.ui

import android.content.Context
import android.content.Intent
import androidx.compose.runtime.Composable
import androidx.compose.runtime.remember
import androidx.compose.ui.platform.LocalContext

/**
 * Hands a post to the system share sheet.
 *
 * WHAT IS SHARED, AND WHY IT IS NOT A LINK
 *
 * The text and, when it is known, the author — as plain text. There is
 * deliberately no URL: the backend exposes no canonical public address for a
 * post, and the app declares no App Link, so any URL here would be one this
 * code invented. A share that lands on a 404 is worse than one carrying only
 * words, because the recipient blames the sender.
 *
 * When a public post route and a verified App Link exist, the link belongs in
 * [Intent.EXTRA_TEXT] alongside the quote — and it should arrive on the post
 * payload rather than being assembled here from a base URL, so that one
 * service owns what a post's address is.
 *
 * Uses the system chooser rather than a bespoke sheet: users already know it,
 * it ranks targets by their own habits, and every app they might send to is
 * reachable without this app enumerating installed packages.
 */
@Composable
fun rememberPostSharer(): (text: String, authorName: String?) -> Unit {
    val context = LocalContext.current
    return remember(context) { { text, author -> context.sharePost(text, author) } }
}

/**
 * [authorName] is nullable because not every payload carries one — the post
 * detail response sends only an author id. Attributing a quote to a UUID would
 * be worse than not attributing it at all.
 */
private fun Context.sharePost(text: String, authorName: String?) {
    val body = when {
        authorName == null -> text
        text.isBlank() -> authorName
        else -> "$text\n\n— $authorName"
    }
    val send = Intent(Intent.ACTION_SEND).apply {
        type = "text/plain"
        putExtra(Intent.EXTRA_TEXT, body)
    }
    // createChooser, not the bare intent: without it Android can silently
    // reuse a previously chosen default, turning "share" into "send to
    // whatever I picked once, months ago".
    startActivity(Intent.createChooser(send, null))
}
