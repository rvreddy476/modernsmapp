package com.us.android.feature.post.createhub.banuba

import android.content.Context
import com.banuba.sdk.core.license.EditorSdk
import com.banuba.sdk.effectplayer.adapter.BanubaEffectPlayerKoinModule
import com.banuba.sdk.export.di.VeExportKoinModule
import com.banuba.sdk.gallery.di.GalleryKoinModule
import com.banuba.sdk.playback.di.VePlaybackSdkKoinModule
import com.banuba.sdk.ve.di.VeSdkKoinModule
import com.banuba.sdk.ve.flow.di.VeFlowKoinModule
import com.banuba.sdk.veui.di.VeUiSdkKoinModule
import dagger.hilt.android.qualifiers.ApplicationContext
import org.koin.android.ext.koin.androidContext
import org.koin.core.context.GlobalContext
import org.koin.core.context.startKoin
import javax.inject.Inject

/**
 * The seam between [BanubaGate] and the vendor SDK, so the gate's state
 * machine is testable without Koin, native libraries or a licence server.
 */
interface BanubaSdk {
    /** Starts the SDK's dependency graph. Idempotent: a running graph is left alone. */
    fun startGraph()

    /** Hands the token to the SDK. Null means the token was empty or truncated. */
    fun initialize(token: String): BanubaLicence?
}

/** An initialised SDK whose licence can be asked about. The answer may take up to a second. */
fun interface BanubaLicence {
    fun check(onState: (valid: Boolean) -> Unit)
}

/**
 * The real SDK behind a Koin graph of its own.
 *
 * Momentum's graph is Hilt; the SDK's is Koin, and it is started here — once,
 * lazily, from the reel flow — never in `Application.onCreate`. The modules
 * are the vendor's, in the vendor's order, with Momentum's overrides last.
 * Absent on purpose: the audio browser and AR cloud modules (paid, unneeded).
 */
class KoinBanubaSdk @Inject constructor(
    @ApplicationContext private val context: Context,
    private val exportTarget: BanubaExportTarget,
) : BanubaSdk {

    override fun startGraph() {
        if (GlobalContext.getOrNull() != null) return
        startKoin {
            androidContext(context)
            allowOverride(true)
            modules(
                VeSdkKoinModule().module,
                VeExportKoinModule().module,
                VePlaybackSdkKoinModule().module,
                VeUiSdkKoinModule().module,
                VeFlowKoinModule().module,
                GalleryKoinModule().module,
                BanubaEffectPlayerKoinModule().module,
                momentumBanubaModule(exportTarget),
            )
        }
    }

    override fun initialize(token: String): BanubaLicence? =
        EditorSdk.initialize(token)?.let { sdk ->
            BanubaLicence { onState -> sdk.getLicenseState { valid -> onState(valid) } }
        }
}
