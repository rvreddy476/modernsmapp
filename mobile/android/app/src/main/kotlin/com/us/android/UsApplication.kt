package com.us.android

import android.app.Application
import coil3.SingletonImageLoader
import com.us.android.core.network.di.AuthenticatedClient
import com.us.android.core.notifications.NotificationChannelSpec
import com.us.android.core.telemetry.Telemetry
import com.us.android.push.PushRegistrationCoordinator
import dagger.Lazy
import dagger.hilt.android.HiltAndroidApp
import okhttp3.OkHttpClient
import javax.inject.Inject

/**
 * Application entry point and Hilt graph root.
 *
 * Deliberately close to empty. Anything expensive here lands directly on cold
 * start, which is a Phase 2 gate. Work that must happen early goes through
 * androidx.startup with an explicit Initializer so its cost is visible and
 * measurable, not hidden in onCreate.
 */
@HiltAndroidApp
class UsApplication : Application() {

    @Inject lateinit var telemetry: Telemetry

    /**
     * Slice D: posts the FCM token once a session exists.
     *
     * Cheap to inject — it holds two references and starts one flow
     * collection. The registrar itself does nothing until there is both a
     * stored token and an authenticated session.
     */
    @Inject lateinit var pushRegistration: PushRegistrationCoordinator

    /**
     * Lazy on purpose. Injecting the client directly would build the whole
     * OkHttp stack during Application.onCreate, on the cold-start path, for a
     * loader that may not be asked for an image until a screen renders.
     */
    @Inject
    @AuthenticatedClient
    lateinit var httpClient: Lazy<OkHttpClient>

    override fun onCreate() {
        super.onCreate()
        installCrashReporter()
        // Cheap and idempotent: creating a channel that already exists is a
        // no-op, and the platform refuses to let re-registration override a
        // user's setting. Done here so a push arriving before any screen
        // opens still has a channel to land on — a notification posted to a
        // missing channel is dropped silently.
        NotificationChannelSpec.createAll(this)
        // Starts a single flow collection. Without this the FCM token is
        // stored and never sent, which is the state the app shipped in.
        pushRegistration.start()
        // setSafe, not setUnsafe: this is a lambda, so the loader — and the
        // OkHttp client behind it — is built on first image request rather
        // than on the cold-start path.
        SingletonImageLoader.setSafe { context -> buildImageLoader(context, httpClient.get()) }
    }

    /**
     * Reports uncaught exceptions before the process dies.
     *
     * ⚠ This is NOT a substitute for a real crash-reporting product. It has no
     * symbolication, no deduplication, no grouping, and — most importantly —
     * export is best-effort: the OTLP batch processor may not flush before the
     * process is killed, so some crashes will be lost. It exists so a crash is
     * *usually* visible rather than *never*.
     *
     * A symbolicated, reliable crash pipeline needs a provisioned product
     * (Crashlytics or equivalent). That is a founder decision with a GCP
     * approval attached per decision-001, and is tracked as audit debt.
     *
     * The previous handler is always delegated to, so the platform still gets
     * to record the crash and show the dialog.
     */
    private fun installCrashReporter() {
        val previous = Thread.getDefaultUncaughtExceptionHandler()
        Thread.setDefaultUncaughtExceptionHandler { thread, throwable ->
            runCatching {
                telemetry.recordError(
                    event = "app.crash",
                    cause = throwable,
                    attributes = mapOf(
                        "thread.name" to thread.name,
                        "exception.type" to throwable.javaClass.name,
                    ),
                )
            }
            // Never swallow: the platform handler is what actually terminates
            // the process and records the ANR/crash for the OS.
            previous?.uncaughtException(thread, throwable)
        }
    }
}
