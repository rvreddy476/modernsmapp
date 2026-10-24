package com.us.android.core.feed.di

import com.us.android.core.feed.data.AudioTracksApi
import com.us.android.core.feed.data.ChannelApi
import com.us.android.core.feed.data.DiscoveryApi
import com.us.android.core.feed.data.FeedApi
import com.us.android.core.feed.data.FeedItemHydrator
import com.us.android.core.feed.data.HashtagPostHydrator
import com.us.android.core.feed.data.HiddenAuthorsApi
import com.us.android.core.feed.data.HiddenAuthorsRepository
import com.us.android.core.feed.data.HiddenAuthorsSource
import com.us.android.core.feed.data.PostEditApi
import com.us.android.core.feed.data.SoundsApi
import com.us.android.core.feed.data.TrendingSoundsApi
import com.us.android.core.feed.data.VideoCollectionSource
import com.us.android.core.feed.data.VideoFeedApi
import com.us.android.core.feed.data.VideoLibraryApi
import com.us.android.core.feed.data.VideoLibraryRepository
import com.us.android.core.feed.data.VideoLibraryWrites
import com.us.android.core.feed.data.WatchHistoryApi
import com.us.android.core.feed.data.WatchHistoryRepository
import com.us.android.core.feed.data.WatchHistorySource
import com.us.android.core.feed.data.action.ActionApi
import com.us.android.core.feed.data.copyright.CopyrightReviewApi
import com.us.android.core.feed.data.credits.CreditsApi
import com.us.android.core.feed.ui.sound.LibrarySoundPreview
import com.us.android.core.feed.ui.sound.PlayerLibrarySoundPreview
import com.us.android.core.feed.ui.sound.PlayerTrendingSoundPreview
import com.us.android.core.feed.ui.sound.TrendingSoundPreview
import com.us.android.core.media.sound.SoundPreviewPlayer
import dagger.Module
import dagger.Provides
import dagger.hilt.InstallIn
import dagger.hilt.components.SingletonComponent
import retrofit2.Retrofit
import javax.inject.Singleton

/** Creates the feed endpoints from the app-wide [Retrofit]. No second stack. */
@Module
@InstallIn(SingletonComponent::class)
object FeedModule {

    @Provides
    @Singleton
    fun provideFeedApi(retrofit: Retrofit): FeedApi = retrofit.create(FeedApi::class.java)

    @Provides
    @Singleton
    fun provideVideoFeedApi(retrofit: Retrofit): VideoFeedApi = retrofit.create(VideoFeedApi::class.java)

    @Provides
    @Singleton
    fun provideChannelApi(retrofit: Retrofit): ChannelApi = retrofit.create(ChannelApi::class.java)

    @Provides
    @Singleton
    fun provideSoundsApi(retrofit: Retrofit): SoundsApi = retrofit.create(SoundsApi::class.java)

    /** Trending sounds (2026-10-24): media-service's ranking, and a sound's own place on it. */
    @Provides
    @Singleton
    fun provideTrendingSoundsApi(retrofit: Retrofit): TrendingSoundsApi = retrofit.create(TrendingSoundsApi::class.java)

    @Provides
    @Singleton
    fun provideVideoLibraryApi(retrofit: Retrofit): VideoLibraryApi = retrofit.create(VideoLibraryApi::class.java)

    /** Tube's History (2026-10-22): post-service's watch record. */
    @Provides
    @Singleton
    fun provideWatchHistoryApi(retrofit: Retrofit): WatchHistoryApi = retrofit.create(WatchHistoryApi::class.java)

    /** Settings › Hidden channels (2026-10-22): feed-service's "Don't recommend" list. */
    @Provides
    @Singleton
    fun provideHiddenAuthorsApi(retrofit: Retrofit): HiddenAuthorsApi = retrofit.create(HiddenAuthorsApi::class.java)

    /** A collection's page reads and edits through the repository; tests hand it a fake. */
    @Provides
    fun provideVideoCollectionSource(impl: VideoLibraryRepository): VideoCollectionSource = impl

    /** The History page reads through the repository; tests hand it a fake. */
    @Provides
    fun provideWatchHistorySource(impl: WatchHistoryRepository): WatchHistorySource = impl

    /** The Hidden channels page reads through the repository; tests hand it a fake. */
    @Provides
    fun provideHiddenAuthorsSource(impl: HiddenAuthorsRepository): HiddenAuthorsSource = impl

    /** media-service's alternate audio tracks (2026-10-21): the picker and the creator's dub manager. */
    @Provides
    @Singleton
    fun provideAudioTracksApi(retrofit: Retrofit): AudioTracksApi = retrofit.create(AudioTracksApi::class.java)

    /** The owner's edit of a published post (2026-10-21). */
    @Provides
    @Singleton
    fun providePostEditApi(retrofit: Retrofit): PostEditApi = retrofit.create(PostEditApi::class.java)

    /** Video credits (2026-10-23): genres, credit requests and "Appears in". */
    @Provides
    @Singleton
    fun provideCreditsApi(retrofit: Retrofit): CreditsApi = retrofit.create(CreditsApi::class.java)

    /** The Action button (2026-10-23): labels, the link a tap opens, the creator's taps. */
    @Provides
    @Singleton
    fun provideActionApi(retrofit: Retrofit): ActionApi = retrofit.create(ActionApi::class.java)

    /** Copyright title review (2026-10-24): the author's "Request review" and the detail re-read. */
    @Provides
    @Singleton
    fun provideCopyrightReviewApi(retrofit: Retrofit): CopyrightReviewApi = retrofit.create(CopyrightReviewApi::class.java)

    /** Discovery (2026-10-24): Reels Explore, Tube Trending and Topics, end screens and cards. */
    @Provides
    @Singleton
    fun provideDiscoveryApi(retrofit: Retrofit): DiscoveryApi = retrofit.create(DiscoveryApi::class.java)

    /** The store writes through the repository; tests hand it a fake. */
    @Provides
    fun provideVideoLibraryWrites(impl: VideoLibraryRepository): VideoLibraryWrites = impl

    /** The real hydrator behind the seam; tests bind the identity instead. */
    @Provides
    fun provideFeedItemHydrator(impl: HashtagPostHydrator): FeedItemHydrator = impl

    /**
     * The Library list's one preview player (library sounds, 2026-10-15).
     * Unscoped on purpose: every list gets its own player, released by the
     * ViewModel that holds it.
     */
    @Provides
    fun provideLibrarySoundPreview(player: SoundPreviewPlayer): LibrarySoundPreview = PlayerLibrarySoundPreview(player)

    /** The Trending list's preview player (2026-10-24): unscoped for the same reason, one per list. */
    @Provides
    fun provideTrendingSoundPreview(player: SoundPreviewPlayer): TrendingSoundPreview =
        PlayerTrendingSoundPreview(player)
}
