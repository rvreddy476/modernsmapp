package com.us.android.core.common.window

import android.view.Window
import android.view.WindowManager
import java.util.WeakHashMap

/**
 * FLAG_SECURE, shared fairly.
 *
 * More than one screen may want the window kept out of screenshots and the
 * recents snapshot at once — the chat lock, and Pulse's screens that show
 * other people — and their lifetimes overlap: during a navigation the screen
 * arriving is composed before the one leaving is disposed. A plain
 * add-on-enter / clear-on-leave then lets the screen that leaves clear the
 * flag the arriving one still needs.
 *
 * So every holder acquires and releases through ONE count per window, and the
 * flag is cleared only when the last holder lets go AND this count was the one
 * that set it: a flag that was already on when the first holder arrived
 * belongs to someone else and is left alone.
 *
 * Main thread only, like the window itself.
 */
object SecureWindow {

    private val counters = WeakHashMap<Window, SecureFlagCounter>()

    /** Holds FLAG_SECURE on [window] until the returned hold is released. */
    fun hold(window: Window): SecureHold =
        counters.getOrPut(window) { SecureFlagCounter(WindowSecureTarget(window)) }.acquire()
}

/** The window seam, so the counting is testable without a window. */
interface SecureFlagTarget {
    val isSecure: Boolean

    fun setSecure()

    fun clearSecure()
}

/** One holder's claim. Releasing twice is the same as once. */
fun interface SecureHold {
    fun release()
}

/** The count for one window. See [SecureWindow]. */
class SecureFlagCounter(private val target: SecureFlagTarget) {

    private var holders = 0

    /** This count turned the flag on, so it may turn it off. */
    private var setHere = false

    val held: Boolean get() = holders > 0

    fun acquire(): SecureHold {
        // Also when someone else cleared it under us: a holder still wants it.
        if (!target.isSecure) {
            target.setSecure()
            setHere = true
        }
        holders++
        var released = false
        return SecureHold {
            if (!released) {
                released = true
                release()
            }
        }
    }

    private fun release() {
        if (holders == 0) return
        holders--
        if (holders == 0) {
            if (setHere && target.isSecure) target.clearSecure()
            setHere = false
        }
    }
}

private class WindowSecureTarget(private val window: Window) : SecureFlagTarget {
    override val isSecure: Boolean
        get() = window.attributes.flags and WindowManager.LayoutParams.FLAG_SECURE != 0

    override fun setSecure() = window.addFlags(WindowManager.LayoutParams.FLAG_SECURE)

    override fun clearSecure() = window.clearFlags(WindowManager.LayoutParams.FLAG_SECURE)
}
