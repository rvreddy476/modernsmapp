package com.us.android.feature.post.createhub

/**
 * The six things the Create sheet can make, in the order the sheet shows them.
 *
 * ## ONE ENUM, TWO CONSUMERS
 *
 * The sheet renders these as tiles; the hub route carries one of them as its
 * argument and opens straight onto that surface. Keeping the tile list and the
 * route vocabulary in one type is what guarantees every tile lands somewhere:
 * a surface with no screen cannot be added here without the hub's `when`
 * failing to compile.
 *
 * [routeKey] is the string on the wire of the navigation graph — stable, lower
 * case, never the enum name, so a rename of a constant cannot break a deep
 * link or a restored back stack. Live is deliberately NOT here: it is a
 * separate row on the sheet that navigates to the live hub, not a composer.
 */
enum class CreateSurface(val routeKey: String, val label: String, val hint: String) {
    Text("text", "Text", "Say something"),
    Photo("photo", "Photo", "Camera or gallery"),
    Reel("reel", "Reel", "Short video"),
    Audio("audio", "Audio", "Voice note or track"),
    Poll("poll", "Poll", "Ask a question"),
    Article("article", "Article", "Long-form"),
    ;

    companion object {
        /**
         * The surface for a route key, or [Text] for anything unknown.
         *
         * Text rather than a crash: a stale key from an old back stack should
         * open SOMETHING useful, and the text composer is the default create.
         */
        fun fromRouteKey(key: String?): CreateSurface =
            entries.firstOrNull { it.routeKey == key } ?: Text
    }
}
