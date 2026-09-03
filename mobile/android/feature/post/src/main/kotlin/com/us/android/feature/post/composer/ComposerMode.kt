package com.us.android.feature.post.composer

/**
 * Which shape the composer wears. Chosen on the Create sheet, fixed for the
 * life of the screen — there is no switch inside.
 *
 * Both post `content_type: post`, `post_type: text`; [Article] adds a required
 * title over a taller body. The bytes differ by exactly one field.
 */
enum class ComposerMode(val title: String) {
    /** A short post: the canvas alone. */
    Post("Text"),

    /** Long-form: a title field, then a body that starts twelve lines tall. */
    Article("Article"),
}
