package com.us.android.core.feed.offline

import android.content.Context
import android.net.ConnectivityManager
import android.net.NetworkCapabilities
import androidx.hilt.work.HiltWorker
import androidx.work.Constraints
import androidx.work.CoroutineWorker
import androidx.work.ExistingPeriodicWorkPolicy
import androidx.work.ExistingWorkPolicy
import androidx.work.NetworkType
import androidx.work.OneTimeWorkRequestBuilder
import androidx.work.PeriodicWorkRequestBuilder
import androidx.work.WorkManager
import androidx.work.WorkerParameters
import com.us.android.core.common.session.SessionTeardownTask
import dagger.assisted.Assisted
import dagger.assisted.AssistedInject
import dagger.hilt.android.qualifiers.ApplicationContext
import java.util.concurrent.TimeUnit
import javax.inject.Inject
import javax.inject.Singleton

/** What the device is connected through, read when it is asked. */
interface OfflineConnectivity {
    fun current(): OfflineConnection
}

/**
 * The platform's answer. "Metered" is the system's own judgement (mobile
 * data, or a Wi-Fi network the user marked as metered), which is the one
 * Media3's fetch requirement uses too, so the line the viewer is told and
 * what the fetch does cannot disagree.
 */
@Singleton
class AndroidOfflineConnectivity @Inject constructor(
    @ApplicationContext private val context: Context,
) : OfflineConnectivity {
    override fun current(): OfflineConnection {
        val manager = context.getSystemService(ConnectivityManager::class.java) ?: return OfflineConnection.NONE
        val capabilities = manager.getNetworkCapabilities(manager.activeNetwork) ?: return OfflineConnection.NONE
        return when {
            !capabilities.hasCapability(NetworkCapabilities.NET_CAPABILITY_INTERNET) -> OfflineConnection.NONE
            manager.isActiveNetworkMetered -> OfflineConnection.METERED
            else -> OfflineConnection.UNMETERED
        }
    }
}

/**
 * Asks the server which copies may still be kept, deletes the ones that may
 * not, renews the rest, and deletes copies a signed-out account left more
 * than 48 hours ago ([OfflineCopies.refresh]).
 *
 * Deliberately dumb, like `AnalyticsUploadWorker`: every rule is the state
 * machine's. Always a success: a check that could not be made changes
 * nothing, and the next run asks again.
 */
@HiltWorker
class OfflineCheckWorker @AssistedInject constructor(
    @Assisted appContext: Context,
    @Assisted params: WorkerParameters,
    private val copies: OfflineCopies,
) : CoroutineWorker(appContext, params) {

    override suspend fun doWork(): Result {
        runCatching { copies.refresh() }
        return Result.success()
    }
}

/**
 * Two jobs, both waiting for a network: one now (so a phone that was
 * offline at launch checks the moment it is back), one every
 * [PERIOD_HOURS] hours. `WorkManager.getInstance`, not an injected one, for
 * the reason `AnalyticsModule` gives: the only `@Provides WorkManager` is
 * `:core:chat`'s, and asking for it here would make every graph that
 * reaches the feed require chat.
 */
@Singleton
class WorkManagerOfflineCheckScheduler @Inject constructor(
    @ApplicationContext private val context: Context,
) : OfflineCheckScheduler {

    private val workManager: WorkManager get() = WorkManager.getInstance(context)

    private val online = Constraints.Builder().setRequiredNetworkType(NetworkType.CONNECTED).build()

    override fun schedule() {
        workManager.enqueueUniqueWork(
            CHECK_NOW,
            ExistingWorkPolicy.KEEP,
            OneTimeWorkRequestBuilder<OfflineCheckWorker>().setConstraints(online).build(),
        )
        workManager.enqueueUniquePeriodicWork(
            CHECK_DAILY,
            ExistingPeriodicWorkPolicy.KEEP,
            PeriodicWorkRequestBuilder<OfflineCheckWorker>(PERIOD_HOURS, TimeUnit.HOURS)
                .setConstraints(online)
                .build(),
        )
    }

    override fun cancel() {
        workManager.cancelUniqueWork(CHECK_NOW)
        workManager.cancelUniqueWork(CHECK_DAILY)
    }

    private companion object {
        const val CHECK_NOW = "offline-copies-check-now"
        const val CHECK_DAILY = "offline-copies-check-daily"

        /** Well inside the server's two-day recheck interval. */
        const val PERIOD_HOURS = 12L
    }
}

/**
 * Sign-out (founder, 2026-10-02): this device's copies are kept for 48
 * hours, stamped and hidden from everyone ([OfflineCopies.holdForSignOut]);
 * a save still in flight is cancelled and the server told while the session
 * is still valid.
 *
 * Failure is not fatal by contract ([SessionTeardownTask]): the stamp is
 * local, a stamp that could not be written is put back on the next start,
 * and nothing here throws.
 */
@Singleton
class OfflineTeardown @Inject constructor(
    private val copies: OfflineCopies,
) : SessionTeardownTask {
    override suspend fun onSignOut() {
        runCatching { copies.holdForSignOut() }
    }
}
