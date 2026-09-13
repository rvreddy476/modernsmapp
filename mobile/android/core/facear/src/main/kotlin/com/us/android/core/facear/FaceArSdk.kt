package com.us.android.core.facear

import android.content.Context
import com.banuba.sdk.license_utils.LicenseManager
import com.banuba.sdk.license_utils.LicenseStatus
import com.banuba.sdk.manager.BanubaSdkManager
import dagger.hilt.android.qualifiers.ApplicationContext
import javax.inject.Inject
import javax.inject.Singleton

/**
 * The seam between [FaceArGate] and the vendor SDK, so the gate's state
 * machine is testable without native libraries, a camera or a licence server.
 *
 * Three questions rather than two, unlike the reel studio's `BanubaSdk`,
 * because two Banuba product lines share this process: the gate must be able
 * to ask "has something already brought the native SDK up?" before deciding
 * whether to bring it up itself.
 */
interface FaceArSdk {
    /**
     * True when this process already holds a Banuba licence — because a
     * previous Face AR start did, or because another product line's path got
     * there first.
     *
     * The gate treats true as "read the licence, do not initialise again".
     */
    fun alreadyInitialised(): Boolean

    /**
     * Brings the native SDK up with [token] and returns the licence it now
     * holds, or null when the token was rejected as empty or truncated.
     */
    fun initialize(token: String): FaceArLicence?

    /** The licence for [token], without bringing anything up that already is. */
    fun licence(token: String): FaceArLicence?
}

/** An initialised licence that can be asked whether it is still good. */
fun interface FaceArLicence {
    fun check(onState: (valid: Boolean) -> Unit)
}

/**
 * The real Face AR SDK.
 *
 * ## WHAT THE TWO INIT PATHS ACTUALLY DO — disassembled, not assumed
 *
 * Two Banuba product lines live in this app on one licence token:
 *
 *  * the reel studio starts the VIDEO EDITOR — `EditorSdk.initialize(token)`
 *    in `:feature:post`, which touches only core-sdk's own
 *    `BanubaLicenseManager`. Licence bookkeeping, no native effect player.
 *  * try-on starts FACE AR — [BanubaSdkManager.initialize], which does exactly
 *    three things: `ReLinker.loadLibrary("banuba")`,
 *    `ContextProvider.setContext`, and `UtilityManager.initialize(paths,
 *    token)`.
 *
 * `BanubaSdkManager.initialize` also returns immediately if it has already run
 * (`sUserResourcesPathsList != null`), so it is idempotent in the vendor's own
 * code, and ReLinker caches the library load. **Calling both product lines'
 * init in one process is therefore LOW risk**, and the guards here are a belt
 * on top of the vendor's braces rather than the load-bearing part.
 *
 * `deinitialize()` is still NEVER called on any path. It would pull the native
 * library out from under a reel editor rendering in the same process, and
 * nothing in this app has a reason to give the licence back mid-session.
 *
 * ## THE HAZARD THAT IS REAL: THE CAMERA AND THE GL SURFACE
 *
 * The video editor's Koin graph creates its own effect player, and a try-on
 * screen creates another. Two players contending for the front camera and a GL
 * context is the realistic failure, and it presents as a **black preview or a
 * crash — never a licence error.** That is why `FaceArSession` closes the
 * camera and pauses the player on every lifecycle stop and recycles on
 * dispose: a try-on screen must hold no camera the moment it is not resumed.
 *
 * **The device check, in both orders:** open the reel camera, back out, open a
 * product try-on — then the reverse. A black preview on the second one is the
 * contention, not the licence.
 *
 * ## WHERE EFFECT BUNDLES ARE RESOLVED FROM
 *
 * No extra resource paths are passed to [BanubaSdkManager.initialize]. Its
 * vararg exists to ADD asset roots; the SDK's own default is
 * `getResourcesBase()` (`"bnb-resources"`) plus
 * [BanubaSdkManager.EFFECTS_RESOURCES_PATH] (`"/effects"`), so a bundle at
 * `assets/bnb-resources/effects/<slug>` is found with no configuration and is
 * loaded by the relative path `effects/<slug>`. Adding a root of our own would
 * be a second place for the layout to be stated. See this module's README.
 */
@Singleton
class BanubaFaceArSdk @Inject constructor(
    @ApplicationContext private val context: Context,
) : FaceArSdk {

    override fun alreadyInitialised(): Boolean =
        runCatching { LicenseManager.instance() != null }.getOrDefault(false)

    override fun initialize(token: String): FaceArLicence? {
        // No vararg: the vendor's default resources base is exactly where this
        // module's README says bundles go. Idempotent in the SDK itself.
        BanubaSdkManager.initialize(context, token)
        return licence(token)
    }

    override fun licence(token: String): FaceArLicence? =
        runCatching { LicenseManager.instance() ?: LicenseManager.create(token) }
            .getOrNull()
            ?.let { manager ->
                // isExpired() answers with a status, not a boolean: VALID is
                // the only one that may render an effect. REVOKED and
                // TIME_BOMBED both mean "a licence exists and is no longer
                // good", which is FaceArState.Invalid rather than Failed.
                FaceArLicence { onState ->
                    val status = runCatching { manager.isExpired() }.getOrNull()
                    onState(status == LicenseStatus.VALID)
                }
            }
}
