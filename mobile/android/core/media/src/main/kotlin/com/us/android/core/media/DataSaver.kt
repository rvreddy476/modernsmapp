package com.us.android.core.media

import android.content.Context
import android.net.ConnectivityManager
import androidx.annotation.OptIn
import androidx.media3.common.Player
import androidx.media3.common.util.UnstableApi
import com.us.android.core.common.di.ApplicationScope
import com.us.android.core.datastore.DataSaverStore
import dagger.hilt.android.qualifiers.ApplicationContext
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.launch
import java.util.concurrent.atomic.AtomicBoolean
import javax.inject.Inject
import javax.inject.Singleton

/**
 * Data saver (2026-10-24, the web's useDataSaver): when the viewer turned it
 * on AND the phone is on a metered network (mobile data, a hotspot), video
 * plays at [DATA_SAVER_MAX_HEIGHT] at most — the ABR ladder is capped, the
 * stream stays the same one — and the Home feed does not start a video by
 * itself. On Wi-Fi nothing changes. Read at the moment a player is prepared
 * or a page settles, so a network change takes effect on the next video.
 */
interface DataSaverGate {
    val active: Boolean
}

/** No data saver: what a host that never wires one gets. */
object NoDataSaver : DataSaverGate {
    override val active: Boolean get() = false
}

/** The rule, pure: the switch, and a metered network. */
fun dataSaverActive(enabled: Boolean, metered: Boolean): Boolean = enabled && metered

/** The tallest rendition data saver allows. */
const val DATA_SAVER_MAX_HEIGHT = 480

/**
 * The height cap a prepared player gets: [DATA_SAVER_MAX_HEIGHT] while data
 * saver is [active], never RAISING a lower cap the viewer picked (a Quality
 * choice of 360p stays 360p); unchanged (null) when it is not active, so a
 * viewer's own Quality pick is never undone by the switch being off.
 */
fun dataSaverHeightCap(active: Boolean, currentMaxHeight: Int): Int? =
    if (active && currentMaxHeight > DATA_SAVER_MAX_HEIGHT) DATA_SAVER_MAX_HEIGHT else null

/** Applies [dataSaverHeightCap] to this player's track selection. */
@OptIn(UnstableApi::class)
fun Player.applyDataSaver(active: Boolean) {
    val before = trackSelectionParameters
    val cap = dataSaverHeightCap(active, before.maxVideoHeight) ?: return
    trackSelectionParameters = before.buildUpon().setMaxVideoSize(before.maxVideoWidth, cap).build()
}

/** The production gate: the stored switch, kept current, and the system's metered flag. */
@Singleton
class DataSaverPolicy @Inject constructor(
    @ApplicationContext context: Context,
    store: DataSaverStore,
    @ApplicationScope scope: CoroutineScope,
) : DataSaverGate {

    private val enabled = AtomicBoolean(false)
    private val connectivity = context.getSystemService(ConnectivityManager::class.java)

    init {
        scope.launch { store.enabled.collect { enabled.set(it) } }
    }

    override val active: Boolean
        get() = dataSaverActive(enabled.get(), connectivity?.isActiveNetworkMetered == true)
}
