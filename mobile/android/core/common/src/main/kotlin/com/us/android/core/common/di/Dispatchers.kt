package com.us.android.core.common.di

import dagger.Module
import dagger.Provides
import dagger.hilt.InstallIn
import dagger.hilt.components.SingletonComponent
import kotlinx.coroutines.CoroutineDispatcher
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.SupervisorJob
import kotlinx.coroutines.plus
import javax.inject.Qualifier
import javax.inject.Singleton
import kotlinx.coroutines.Dispatchers as KotlinDispatchers

/**
 * Dispatchers are injected, never referenced statically.
 *
 * A repository that hard-codes `Dispatchers.IO` cannot be unit-tested
 * deterministically; one that takes an injected [UsDispatcher] can be handed
 * a TestDispatcher and run in virtual time.
 */
@Qualifier
@Retention(AnnotationRetention.RUNTIME)
annotation class Dispatcher(val dispatcher: UsDispatcher)

enum class UsDispatcher { Default, IO, Main }

@Module
@InstallIn(SingletonComponent::class)
object DispatchersModule {
    @Provides
    @Dispatcher(UsDispatcher.Default)
    fun providesDefaultDispatcher(): CoroutineDispatcher = KotlinDispatchers.Default

    @Provides
    @Dispatcher(UsDispatcher.IO)
    fun providesIoDispatcher(): CoroutineDispatcher = KotlinDispatchers.IO

    @Provides
    @Dispatcher(UsDispatcher.Main)
    fun providesMainDispatcher(): CoroutineDispatcher = KotlinDispatchers.Main.immediate
}

/**
 * Scope for work that must outlive any single screen — session restore,
 * analytics flush, outbox drain. SupervisorJob so one failure does not
 * cancel siblings.
 */
@Qualifier
@Retention(AnnotationRetention.RUNTIME)
annotation class ApplicationScope

@Module
@InstallIn(SingletonComponent::class)
object CoroutineScopeModule {
    @Provides
    @Singleton
    @ApplicationScope
    fun providesApplicationScope(
        @Dispatcher(UsDispatcher.Default) dispatcher: CoroutineDispatcher,
    ): CoroutineScope = CoroutineScope(SupervisorJob() + dispatcher)
}
