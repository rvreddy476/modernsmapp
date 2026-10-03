package com.us.android.core.media.offline

import com.google.common.truth.Truth.assertThat
import mockwebserver3.MockResponse
import mockwebserver3.MockWebServer
import okhttp3.OkHttpClient
import org.junit.After
import org.junit.Before
import org.junit.Rule
import org.junit.Test
import org.junit.rules.TemporaryFolder
import java.io.File

/**
 * The small files beside a copy (its poster, its caption tracks).
 *
 * What this protects: a file lands whole or not at all (a reader never
 * finds half a caption track), a failed fetch leaves nothing behind, and a
 * name can never place a file outside the folder it was given.
 */
class OfflineFilesTest {

    @get:Rule
    val folder = TemporaryFolder()

    private lateinit var server: MockWebServer
    private lateinit var files: OfflineFiles
    private val root: File get() = File(folder.root, "files")

    @Before
    fun setUp() {
        server = MockWebServer()
        server.start()
        files = OfflineFiles(root = { root }, client = { OkHttpClient() })
    }

    @After
    fun tearDown() = server.close()

    private fun url(path: String) = server.url(path).toString()

    @Test
    fun `a fetched file is stored whole under its post's folder`() {
        server.enqueue(MockResponse.Builder().body("WEBVTT\n\n00:00.000 --> 00:01.000\nhello").build())

        val stored = files.fetch(url("/v1/subtitles/m1/track/en.vtt"), folder = "p1", name = "captions_en.vtt")

        assertThat(stored).isEqualTo(File(File(root, "p1"), "captions_en.vtt"))
        assertThat(stored!!.readText()).startsWith("WEBVTT")
        assertThat(File(root, "p1").list()!!.toList()).containsExactly("captions_en.vtt")
        assertThat(server.takeRequest().target).isEqualTo("/v1/subtitles/m1/track/en.vtt")
    }

    @Test
    fun `a refused fetch stores nothing and leaves no partial file`() {
        server.enqueue(MockResponse.Builder().code(404).body("gone").build())

        val stored = files.fetch(url("/poster"), folder = "p1", name = "poster")

        assertThat(stored).isNull()
        assertThat(File(root, "p1").list().orEmpty().toList()).isEmpty()
    }

    @Test
    fun `an empty answer is not a file`() {
        server.enqueue(MockResponse.Builder().body("").build())

        assertThat(files.fetch(url("/poster"), folder = "p1", name = "poster")).isNull()
        assertThat(File(root, "p1").list().orEmpty().toList()).isEmpty()
    }

    @Test
    fun `something that is not an address stores nothing`() {
        assertThat(files.fetch("not a url", folder = "p1", name = "poster")).isNull()
    }

    @Test
    fun `a name cannot place a file outside its folder`() {
        val hostile = files.file(folder = "../../outside", name = "../../../etc/passwd")

        assertThat(hostile.canonicalPath).startsWith(root.canonicalPath + File.separator)
        assertThat(hostile.parentFile!!.parentFile!!.canonicalFile).isEqualTo(root.canonicalFile)
    }

    @Test
    fun `removing a post's folder takes its files and leaves the others`() {
        server.enqueue(MockResponse.Builder().body("a").build())
        server.enqueue(MockResponse.Builder().body("bb").build())
        files.fetch(url("/a"), folder = "p1", name = "poster")
        files.fetch(url("/b"), folder = "p2", name = "poster")

        files.remove("p1")

        assertThat(File(root, "p1").exists()).isFalse()
        assertThat(files.usedBytes()).isEqualTo(2L)

        files.removeAll()

        assertThat(root.exists()).isFalse()
        assertThat(files.usedBytes()).isEqualTo(0L)
    }
}
