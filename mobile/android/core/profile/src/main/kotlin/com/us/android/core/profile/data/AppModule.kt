package com.us.android.core.profile.data

/**
 * A product module the user can switch on or off — the privacy-first,
 * server-driven experience picker.
 *
 * [id] is the wire contract with user-service (`GET/PUT /v1/users/me/modules`)
 * and must never change once published. [hasScreen] is a CLIENT fact: whether
 * this build ships a surface for the module. A module without a screen is
 * still recorded server-side — the choice survives until the build catches up
 * — but it must never become a tab, because a tab that opens nothing is a lie.
 *
 * [FEED] is special: it is not one of the seven optional modules the server
 * accepts in `modules`, but it IS a valid `home_module` and the tab every
 * user always has. Modelled here so a home choice is one type, not a string.
 */
enum class AppModule(
    val id: String,
    val displayName: String,
    val hasScreen: Boolean,
) {
    FEED("feed", "Home feed", hasScreen = true),
    REELS("reels", "Reels", hasScreen = true),
    COMMERCE("commerce", "Commerce", hasScreen = false),
    CHAT("chat", "Chat", hasScreen = true),
    DATING("dating", "Dating", hasScreen = false),
    FOOD("food", "Food", hasScreen = false),
    QA("qa", "QA", hasScreen = false),
    POSTTUBE("posttube", "PostTube", hasScreen = true),
    ;

    companion object {
        /** The seven optional modules the onboarding screen offers. */
        val selectable: List<AppModule> = entries.filter { it != FEED }

        fun fromId(id: String): AppModule? = entries.firstOrNull { it.id == id }
    }
}
