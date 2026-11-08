package com.us.android.core.notifications

/**
 * An app's own handling of an operational push, run before the generic
 * [NotificationPresenter] (food audit M4). Feast Kitchen binds one so a
 * data-only `food_order_new` rings its looping new-order alarm even while the
 * process was cached; every other app binds nothing and the push path is
 * unchanged.
 *
 * Runs on FCM's thread with the push still in hand, so it must return at once
 * and never call the network. Returns true when it handled the push fully and
 * the presenter must not also draw it.
 */
fun interface OperationalPushHandler {
    fun onPush(data: Map<String, String>): Boolean
}

/** Optional: only an app with an operational alarm binds an [OperationalPushHandler]. */
@dagger.Module
@dagger.hilt.InstallIn(dagger.hilt.components.SingletonComponent::class)
abstract class OperationalPushHandlerModule {
    @dagger.BindsOptionalOf
    abstract fun optionalOperationalPushHandler(): OperationalPushHandler
}
