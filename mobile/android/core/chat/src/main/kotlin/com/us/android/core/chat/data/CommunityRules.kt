package com.us.android.core.chat.data

/**
 * The create/edit form's rules, mirrored from the contract so the sheet can
 * say no BEFORE a round trip: name ≤ 60, handle `^[a-z0-9_]{3,30}$`,
 * description ≤ 300, update body ≤ 2000. The server stays authoritative —
 * a 422 still lands as a message — these only stop the obvious.
 */
object CommunityRules {
    const val NAME_MAX = 60
    const val HANDLE_MIN = 3
    const val HANDLE_MAX = 30
    const val DESCRIPTION_MAX = 300
    const val UPDATE_BODY_MAX = 2000
    const val GROUP_DESCRIPTION_MAX = 300

    private val HANDLE = Regex("^[a-z0-9_]{3,30}$")

    /** Null when the handle is well-formed; otherwise what to tell the user. */
    fun handleProblem(handle: String): String? = when {
        handle.isBlank() -> "Pick a handle."
        handle.length < HANDLE_MIN -> "At least $HANDLE_MIN characters."
        handle.length > HANDLE_MAX -> "At most $HANDLE_MAX characters."
        !HANDLE.matches(handle) -> "Lowercase letters, numbers and _ only."
        else -> null
    }

    fun isHandleValid(handle: String): Boolean = handleProblem(handle) == null

    fun nameProblem(name: String): String? = when {
        name.isBlank() -> "Give it a name."
        name.length > NAME_MAX -> "At most $NAME_MAX characters."
        else -> null
    }

    fun descriptionProblem(description: String): String? =
        if (description.length > DESCRIPTION_MAX) "At most $DESCRIPTION_MAX characters." else null

    fun bodyProblem(body: String): String? = when {
        body.isBlank() -> "Write something first."
        body.length > UPDATE_BODY_MAX -> "At most $UPDATE_BODY_MAX characters."
        else -> null
    }

    /** What the user typed, coerced toward a legal handle: lowercase, spaces to underscores. */
    fun normaliseHandle(raw: String): String =
        raw.lowercase().replace(' ', '_').filter { it.isLetterOrDigit() || it == '_' }.take(HANDLE_MAX)
}
