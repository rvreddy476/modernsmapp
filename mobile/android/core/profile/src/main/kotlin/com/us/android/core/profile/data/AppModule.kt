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
    COMMERCE("commerce", "Commerce", hasScreen = true),
    CHAT("chat", "Chat", hasScreen = true),
    DATING("dating", "Dating", hasScreen = true),
    FOOD("food", "Food", hasScreen = true),
    QA("qa", "QA", hasScreen = false),
    POSTTUBE("posttube", "PostTube", hasScreen = true),

    /** Mopedu rides (2026-09-18). The id is PROPOSED until user-service accepts it in `modules`. */
    MOBILITY("mobility", "Mobility", hasScreen = true),

    /**
     * Doorstep home services (2026-10-04). The id is PROPOSED: identity
     * user-service validates `modules` against a closed list (knownModules in
     * internal/service/modules.go and the CHECK constraints on
     * usr.module_preferences), which does not hold "home_services" yet — until
     * it does, a save that includes this module is refused with 400
     * INVALID_MODULE.
     */
    HOME_SERVICES("home_services", "Doorstep", hasScreen = true),
    ;

    companion object {
        /** The optional modules the onboarding screen offers (nine since Doorstep joined). */
        val selectable: List<AppModule> = entries.filter { it != FEED }

        fun fromId(id: String): AppModule? = entries.firstOrNull { it.id == id }
    }
}

/**
 * The parked product behind a module (`GET /v1/features`, founder decision C,
 * 24 Oct 2026), or null for one no gateway switch closes.
 */
val AppModule.product: com.us.android.core.model.Product?
    get() = when (this) {
        AppModule.DATING -> com.us.android.core.model.Product.DATING
        AppModule.FOOD -> com.us.android.core.model.Product.FOOD
        AppModule.QA -> com.us.android.core.model.Product.QA
        AppModule.MOBILITY -> com.us.android.core.model.Product.RIDER
        AppModule.HOME_SERVICES -> com.us.android.core.model.Product.DOORSTEP
        AppModule.FEED, AppModule.REELS, AppModule.COMMERCE, AppModule.CHAT, AppModule.POSTTUBE -> null
    }

/**
 * The modules the picker offers: [AppModule.selectable] without those of a
 * closed product. A choice already saved for a closed one is left as it is
 * and shows again when the product opens. Pure.
 */
fun offeredModules(open: Set<com.us.android.core.model.Product>?): List<AppModule> =
    AppModule.selectable.filter { module -> module.product?.let { com.us.android.core.model.productOpen(it, open) } != false }
