package com.us.android.core.feed.data

import com.google.common.truth.Truth.assertThat
import com.us.android.core.media.PlaybackKind
import org.junit.Test

/**
 * Which audio a video plays for a viewer's language (2026-10-21, the web's
 * `pickAudioTrack`): the ready dub in that language, else the original —
 * never another language, never a dub that is not ready.
 */
class AudioTrackPickTest {

    private val original = AudioTrack("original", "und", "Original", AudioTrackSource.ORIGINAL, AudioTrackStatus.READY)
    private val hindi = AudioTrack(
        id = "t-hi",
        language = "hi",
        label = "Hindi",
        source = AudioTrackSource.UPLOADED,
        status = AudioTrackStatus.READY,
        playbackUrl = "/v1/media/m/serve/dub_hi_720p",
    )
    private val telugu = AudioTrack(
        id = "t-te",
        language = "te",
        label = "",
        source = AudioTrackSource.GENERATED,
        status = AudioTrackStatus.READY,
        playbackUrl = "/v1/media/m/serve/dub_te_720p",
    )
    private val tracks = listOf(original, hindi, telugu)

    @Test
    fun `the preferred language's ready dub is picked`() {
        assertThat(pickAudioTrack(tracks, "hi")).isEqualTo(hindi)
    }

    @Test
    fun `the language match ignores case`() {
        assertThat(pickAudioTrack(tracks, "HI")).isEqualTo(hindi)
    }

    @Test
    fun `no preference plays the original`() {
        assertThat(pickAudioTrack(tracks, null)).isNull()
        assertThat(pickAudioTrack(tracks, "")).isNull()
        assertThat(activeAudioTrackId(tracks, null)).isEqualTo(ORIGINAL_AUDIO_TRACK_ID)
    }

    @Test
    fun `a language the video lacks falls back to the original, not to another dub`() {
        assertThat(pickAudioTrack(tracks, "ta")).isNull()
        assertThat(activeAudioTrackId(tracks, "ta")).isEqualTo(ORIGINAL_AUDIO_TRACK_ID)
    }

    @Test
    fun `a track that is not ready, or has nothing to play, is ignored`() {
        val states = listOf(AudioTrackStatus.PENDING, AudioTrackStatus.PROCESSING, AudioTrackStatus.FAILED)
        states.forEach { status ->
            assertThat(pickAudioTrack(listOf(original, hindi.copy(status = status)), "hi")).isNull()
        }
        assertThat(pickAudioTrack(listOf(original, hindi.copy(playbackUrl = null)), "hi")).isNull()
    }

    @Test
    fun `the original itself is never a dub to switch to`() {
        assertThat(pickAudioTrack(listOf(original.copy(language = "hi", playbackUrl = "/x")), "hi")).isNull()
    }

    @Test
    fun `the picker lists the original first, then each playable dub, naming an unlabelled one by its language`() {
        val pending = hindi.copy(id = "t-ta", language = "ta", status = AudioTrackStatus.PROCESSING)

        val options = audioTrackOptions(tracks + pending)

        assertThat(options.map { it.id }).containsExactly("original", "t-hi", "t-te").inOrder()
        assertThat(options.map { it.label }).containsExactly("Original", "Hindi", "Telugu").inOrder()
    }

    @Test
    fun `a pick stands for its language, and the original for none`() {
        assertThat(languageForChoice(tracks, "t-te")).isEqualTo("te")
        assertThat(languageForChoice(tracks, ORIGINAL_AUDIO_TRACK_ID)).isNull()
        assertThat(languageForChoice(tracks, "gone")).isNull()
    }

    @Test
    fun `a ready dub plays as one progressive MP4 on the gateway`() {
        val playback = dubPlayback(tracks, "hi") { path -> "https://api.example$path" }!!

        assertThat(playback.url).isEqualTo("https://api.example/v1/media/m/serve/dub_hi_720p")
        assertThat(playback.kind).isEqualTo(PlaybackKind.Progressive)
        assertThat(dubPlayback(tracks, "ta") { it }).isNull()
    }

    @Test
    fun `a language's name comes from the list, then the platform, then the tag`() {
        assertThat(audioLanguageLabel("hi")).isEqualTo("Hindi")
        assertThat(audioLanguageLabel("it")).isEqualTo("Italian")
        assertThat(audioLanguageLabel("zzz")).isEqualTo("zzz")
    }

    @Test
    fun `an unknown status is not playable and an unknown source is an upload`() {
        assertThat(AudioTrackStatus.fromWire("transcoding")).isEqualTo(AudioTrackStatus.PENDING)
        assertThat(AudioTrackSource.fromWire("imported")).isEqualTo(AudioTrackSource.UPLOADED)
    }
}
