package com.us.android.core.profile.data

/**
 * Which modules the user switched on, and which one opens first.
 *
 * [modules] never contains [AppModule.FEED] — the feed is not optional on the
 * server either. [homeModule] is FEED or one of [modules]; that invariant is
 * the server's (400 INVALID_HOME_MODULE) and [withHome] keeps the client's
 * edits inside it.
 */
data class ModulePreferences(
    val modules: Set<AppModule>,
    val homeModule: AppModule,
    val onboardingCompleted: Boolean,
) {
    /** Whether [module] should be reachable — FEED always is. */
    fun includes(module: AppModule): Boolean = module == AppModule.FEED || module in modules

    /** The valid home choices: the feed plus whatever is switched on. */
    val homeCandidates: List<AppModule>
        get() = listOf(AppModule.FEED) + AppModule.selectable.filter { it in modules }

    fun withModules(next: Set<AppModule>): ModulePreferences {
        val cleaned = next - AppModule.FEED
        val home = if (homeModule == AppModule.FEED || homeModule in cleaned) homeModule else AppModule.FEED
        return copy(modules = cleaned, homeModule = home)
    }

    fun withHome(next: AppModule): ModulePreferences =
        if (includes(next)) copy(homeModule = next) else this

    companion object {
        /**
         * What the server answers when no row exists: every module on, the
         * feed first, onboarding not yet completed. Also what the shell falls
         * back to when the endpoint is unreachable and nothing is cached —
         * an outage must not push a returning user through onboarding.
         */
        val DEFAULT = ModulePreferences(
            modules = AppModule.selectable.toSet(),
            homeModule = AppModule.FEED,
            onboardingCompleted = false,
        )
    }
}

/** The repository's view of the preferences; the shell gates on it. */
sealed interface ModulePrefsState {
    /** Nothing loaded yet — neither cache nor network has answered. */
    data object Unknown : ModulePrefsState

    /** From the server, or from the cache of the last server answer. */
    data class Loaded(val prefs: ModulePreferences) : ModulePrefsState

    /** The endpoint failed and there was no cache to fall back on. */
    data object Unavailable : ModulePrefsState
}
