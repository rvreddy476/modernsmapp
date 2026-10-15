package com.us.android.core.model

/**
 * The viewer's Appearance setting (TikTok parity T-32, 2026-10-15): follow
 * the device, or keep the app light or dark whatever the device says.
 *
 * [SYSTEM] is the default, and what the app did before the setting existed
 * (founder, 2026-10-02: the app follows the device's light / dark setting).
 * [LIGHT] and [DARK] force the theme. A surface that is dark on purpose — a
 * live room, the reel stage — stays dark whichever is chosen.
 */
enum class AppearanceMode(
    /** What is stored for it; never shown. */
    val wire: String,
    /** What the settings row reads. */
    val label: String,
) {
    SYSTEM("system", "System"),
    LIGHT("light", "Light"),
    DARK("dark", "Dark"),
    ;

    /** Whether the app is dark, given whether the DEVICE is dark: System follows it, Light and Dark ignore it. */
    fun isDark(systemDark: Boolean): Boolean = when (this) {
        SYSTEM -> systemDark
        LIGHT -> false
        DARK -> true
    }

    companion object {
        /** The stored value back to a mode; anything unknown, or nothing stored, is [SYSTEM]. */
        fun fromWire(value: String?): AppearanceMode = entries.firstOrNull { it.wire == value } ?: SYSTEM
    }
}
