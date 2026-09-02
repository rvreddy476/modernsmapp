package com.us.android.feature.notifications.di

import com.us.android.feature.notifications.ui.DefaultNotificationActions
import com.us.android.feature.notifications.ui.NotificationActions
import dagger.Binds
import dagger.Module
import dagger.hilt.InstallIn
import dagger.hilt.components.SingletonComponent

/** Binds the chat + graph backed row actions to the inbox's port. */
@Module
@InstallIn(SingletonComponent::class)
abstract class NotificationsFeatureModule {

    @Binds
    abstract fun bindNotificationActions(impl: DefaultNotificationActions): NotificationActions
}
